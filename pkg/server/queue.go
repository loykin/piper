package server

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

type taskStatus string

const (
	taskPending  taskStatus = "pending"  // dep 대기 중 — 아직 worker에 노출 안 함
	taskReady    taskStatus = "ready"    // dep 완료 — worker가 가져갈 수 있음
	taskRunning  taskStatus = "running"  // worker가 실행 중
	taskDone     taskStatus = "done"
	taskFailed   taskStatus = "failed"
	taskSkipped  taskStatus = "skipped"
)

type taskEntry struct {
	task   *proto.Task
	step   *pipeline.Step
	status taskStatus
	result *proto.TaskResult
}

type run struct {
	id        string
	pipeline  *pipeline.Pipeline
	dag       *pipeline.DAG
	tasks     map[string]*taskEntry // step name → entry
	outputDir string
	workDir   string
}

// Queue는 DAG를 인식하는 task 큐
type Queue struct {
	mu   sync.Mutex
	runs map[string]*run // run id → run
}

func NewQueue() *Queue {
	return &Queue{runs: make(map[string]*run)}
}

// AddRun은 파이프라인 실행을 등록하고 dep 없는 step을 즉시 ready로 만든다
func (q *Queue) AddRun(p *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	r := &run{
		id:        runID,
		pipeline:  p,
		dag:       dag,
		tasks:     make(map[string]*taskEntry),
		outputDir: outputDir,
		workDir:   workDir,
	}

	pipelineJSON, _ := marshalJSON(p)

	for _, step := range p.Spec.Steps {
		s := step // copy
		stepJSON, _ := marshalJSON(&s)
		task := &proto.Task{
			ID:        fmt.Sprintf("%s-%s", runID, s.Name),
			RunID:     runID,
			StepName:  s.Name,
			Step:      stepJSON,
			Pipeline:  pipelineJSON,
			WorkDir:   workDir,
			OutputDir: outputDir,
			CreatedAt: time.Now(),
			Label:     s.Runner.Label,
		}
		entry := &taskEntry{task: task, step: &s, status: taskPending}
		r.tasks[s.Name] = entry
	}

	// dep 없는 step은 즉시 ready
	q.promoteReady(r)
	q.runs[runID] = r
}

// Next는 label에 맞는 ready task를 running으로 바꾸고 반환
func (q *Queue) Next(label string) *proto.Task {
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
			slog.Info("task ready", "task_id", entry.task.ID, "label", label)
			return entry.task
		}
	}
	return nil
}

// Complete는 task 결과를 기록하고 unblock된 step을 ready로 만든다
func (q *Queue) Complete(result *proto.TaskResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	r, entry, err := q.findByTaskID(result.TaskID)
	if err != nil {
		return err
	}

	entry.status = taskStatus(result.Status)
	entry.result = result
	slog.Info("task result", "task_id", result.TaskID, "status", result.Status)

	if result.Status == "done" {
		q.promoteReady(r)
	} else {
		// 실패 시 downstream skip
		q.skipDownstream(r, entry.step.Name)
	}
	return nil
}

// promoteReady는 dep가 모두 done인 pending step을 ready로 전환
func (q *Queue) promoteReady(r *run) {
	done := q.doneNames(r)
	for _, entry := range r.tasks {
		if entry.status != taskPending {
			continue
		}
		if depsAllDone(entry.step.DependsOn, done) {
			entry.status = taskReady
			slog.Info("task ready", "task_id", entry.task.ID)
		}
	}
}

func (q *Queue) skipDownstream(r *run, failedStep string) {
	for _, entry := range r.tasks {
		if entry.status != taskPending && entry.status != taskReady {
			continue
		}
		for _, dep := range entry.step.DependsOn {
			if dep == failedStep {
				entry.status = taskSkipped
				slog.Info("task skipped", "task_id", entry.task.ID, "failed_dep", failedStep)
				q.skipDownstream(r, entry.step.Name) // 재귀적으로 downstream도 skip
				break
			}
		}
	}
}

func (q *Queue) doneNames(r *run) map[string]bool {
	done := make(map[string]bool)
	for name, entry := range r.tasks {
		if entry.status == taskDone || entry.status == taskSkipped {
			done[name] = true
		}
	}
	return done
}

func (q *Queue) findByTaskID(taskID string) (*run, *taskEntry, error) {
	for _, r := range q.runs {
		for _, entry := range r.tasks {
			if entry.task.ID == taskID {
				return r, entry, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("task %s not found", taskID)
}

func depsAllDone(deps []string, done map[string]bool) bool {
	for _, d := range deps {
		if !done[d] {
			return false
		}
	}
	return true
}
