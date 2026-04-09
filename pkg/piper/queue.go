package piper

// 내부 task 큐 — DAG 인식, 분산 worker 실행용.
// Task ID: "{runID}:{stepName}" (콜론 구분자, step 이름에 콜론 없음).
//
// Dispatcher가 설정된 경우 task가 ready 상태가 되는 즉시 Dispatch를 호출한다.
// (K8s Job 등 능동적 dispatch 모드)
// Dispatcher가 nil이면 worker가 /api/tasks/next 폴링으로 task를 가져간다.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/store"
)

type taskStatus string

const (
	taskPending taskStatus = "pending"
	taskReady   taskStatus = "ready"
	taskRunning taskStatus = "running"
	taskDone    taskStatus = "done"
	taskFailed  taskStatus = "failed"
	taskSkipped taskStatus = "skipped"
)

type taskEntry struct {
	task   *proto.Task
	step   *pipeline.Step
	status taskStatus
}

type runEntry struct {
	runID string
	pl    *pipeline.Pipeline
	dag   *pipeline.DAG
	tasks map[string]*taskEntry // stepName → entry
}

type queue struct {
	mu         sync.Mutex
	runs       map[string]*runEntry // runID → entry
	st         *store.Store
	dispatcher proto.Dispatcher // nil이면 폴링 모드
}

func newQueue(st *store.Store) *queue {
	return &queue{runs: make(map[string]*runEntry), st: st}
}

func (q *queue) setDispatcher(d proto.Dispatcher) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dispatcher = d
}

func makeTaskID(runID, stepName string) string {
	return runID + ":" + stepName
}

func splitTaskID(id string) (runID, stepName string, err error) {
	idx := strings.Index(id, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid task id %q: missing colon separator", id)
	}
	return id[:idx], id[idx+1:], nil
}

// add는 파이프라인을 큐에 등록하고 dep 없는 step을 즉시 ready 상태로 만든다.
func (q *queue) add(pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	pipelineJSON, _ := json.Marshal(pl)

	r := &runEntry{
		runID: runID,
		pl:    pl,
		dag:   dag,
		tasks: make(map[string]*taskEntry),
	}

	for i := range pl.Spec.Steps {
		s := &pl.Spec.Steps[i]
		stepJSON, _ := json.Marshal(s)
		task := &proto.Task{
			ID:        makeTaskID(runID, s.Name),
			RunID:     runID,
			StepName:  s.Name,
			Step:      stepJSON,
			Pipeline:  pipelineJSON,
			WorkDir:   workDir,
			OutputDir: outputDir,
			CreatedAt: time.Now(),
			Label:     s.Runner.Label,
		}
		r.tasks[s.Name] = &taskEntry{task: task, step: s, status: taskPending}
	}

	q.promoteReady(r)
	q.runs[runID] = r
}

// next는 label에 맞는 ready task를 running 상태로 바꾸고 반환한다. 없으면 nil.
func (q *queue) next(label string) *proto.Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, r := range q.runs {
		for _, entry := range r.tasks {
			if entry.status != taskReady {
				continue
			}
			if label != "" && entry.task.Label != "" && entry.task.Label != label {
				continue
			}
			entry.status = taskRunning
			slog.Info("task dispatched", "task_id", entry.task.ID, "label", label)
			return entry.task
		}
	}
	return nil
}

// complete는 task 결과를 기록하고 downstream을 처리한다.
func (q *queue) complete(id, status, errMsg string, startedAt, endedAt time.Time, attempts int) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	runID, stepName, err := splitTaskID(id)
	if err != nil {
		return err
	}

	r, ok := q.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found in queue", runID)
	}
	entry, ok := r.tasks[stepName]
	if !ok {
		return fmt.Errorf("step %s not found in run %s", stepName, runID)
	}

	entry.status = taskStatus(status)

	// 스텝 결과 DB 저장
	now := endedAt
	if err := q.st.UpsertStep(&store.Step{
		RunID:     runID,
		StepName:  stepName,
		Status:    status,
		StartedAt: &startedAt,
		EndedAt:   &now,
		Attempts:  attempts,
		Error:     errMsg,
	}); err != nil {
		slog.Warn("upsert step failed", "task_id", id, "err", err)
	}
	slog.Info("task completed", "task_id", id, "status", status)

	if status == string(taskDone) {
		q.promoteReady(r)
	} else {
		q.skipDownstream(r, stepName)
	}

	// 모든 step이 terminal이면 run 완료
	if q.allTerminal(r) {
		runStatus := "success"
		for _, e := range r.tasks {
			if e.status == taskFailed {
				runStatus = "failed"
				break
			}
		}
		finishedAt := time.Now()
		if err := q.st.UpdateRunStatus(runID, runStatus, &finishedAt); err != nil {
			slog.Warn("update run status failed", "run_id", runID, "err", err)
		}
		delete(q.runs, runID)
		slog.Info("run completed", "run_id", runID, "status", runStatus)
	}

	return nil
}

func (q *queue) promoteReady(r *runEntry) {
	done := q.doneNames(r)
	for _, entry := range r.tasks {
		if entry.status != taskPending {
			continue
		}
		if depsAllDone(entry.step.DependsOn, done) {
			entry.status = taskReady
			slog.Info("task ready", "task_id", entry.task.ID)
			q.dispatchIfNeeded(entry)
		}
	}
}

// dispatchIfNeeded는 Dispatcher가 설정된 경우 task를 즉시 dispatch한다.
// lock을 보유한 상태에서 호출되므로 goroutine으로 비동기 실행한다.
func (q *queue) dispatchIfNeeded(entry *taskEntry) {
	if q.dispatcher == nil {
		return
	}
	// 폴링 대신 즉시 dispatch — running 상태로 마킹
	entry.status = taskRunning
	task := entry.task
	go func() {
		if err := q.dispatcher.Dispatch(context.Background(), task); err != nil {
			slog.Error("dispatch failed", "task_id", task.ID, "err", err)
			_ = q.complete(task.ID, "failed", err.Error(), time.Now(), time.Now(), 0)
		}
	}()
}

func (q *queue) skipDownstream(r *runEntry, failedStep string) {
	for _, entry := range r.tasks {
		if entry.status != taskPending && entry.status != taskReady {
			continue
		}
		for _, dep := range entry.step.DependsOn {
			if dep == failedStep {
				entry.status = taskSkipped
				if err := q.st.UpsertStep(&store.Step{
					RunID:    r.runID,
					StepName: entry.step.Name,
					Status:   "skipped",
				}); err != nil {
					slog.Warn("upsert skipped step failed", "task_id", entry.task.ID, "err", err)
				}
				slog.Info("task skipped", "task_id", entry.task.ID, "failed_dep", failedStep)
				q.skipDownstream(r, entry.step.Name)
				break
			}
		}
	}
}

func (q *queue) doneNames(r *runEntry) map[string]bool {
	done := make(map[string]bool)
	for name, entry := range r.tasks {
		if entry.status == taskDone || entry.status == taskSkipped {
			done[name] = true
		}
	}
	return done
}

func (q *queue) allTerminal(r *runEntry) bool {
	for _, entry := range r.tasks {
		switch entry.status {
		case taskDone, taskFailed, taskSkipped:
		default:
			return false
		}
	}
	return true
}

func depsAllDone(deps []string, done map[string]bool) bool {
	for _, d := range deps {
		if !done[d] {
			return false
		}
	}
	return true
}
