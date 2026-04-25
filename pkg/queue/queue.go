package queue

// Internal task queue — DAG-aware, for distributed worker execution.
// Task ID: "{runID}:{stepName}" (colon separator; step names must not contain a colon).
//
// When a Dispatcher is configured, Dispatch is called immediately when a task becomes ready.
// (Active dispatch mode, e.g. K8s Jobs)
// When Dispatcher is nil, workers pull tasks by polling /api/tasks/next.

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
	"github.com/piper/piper/pkg/run"
)

type taskStatus string

const (
	taskPending taskStatus = "pending"
	taskReady   taskStatus = "ready"
	taskRunning taskStatus = "running"
	// taskDone and taskFailed use proto constants to stay in sync with worker/agent reporting.
	taskDone    taskStatus = proto.TaskStatusDone
	taskFailed  taskStatus = proto.TaskStatusFailed
	taskSkipped taskStatus = "skipped"
)

type taskEntry struct {
	task   *proto.Task
	step   *pipeline.Step
	status taskStatus
}

type runEntry struct {
	runID   string
	pl      *pipeline.Pipeline
	dag     *pipeline.DAG
	tasks   map[string]*taskEntry // stepName → entry
	addedAt time.Time
}

// Queue is the DAG-aware task queue for distributed worker execution.
type Queue struct {
	mu           sync.Mutex
	runs         map[string]*runEntry // runID → entry
	runRepo      run.Repository
	stepRepo     run.StepRepository
	dispatcher   proto.Dispatcher                                               // nil means polling mode
	OnRunSuccess func(ctx context.Context, runID string, pl *pipeline.Pipeline) // called (async) when a run succeeds
}

// NewQueue creates a new Queue backed by the given repositories.
func NewQueue(runRepo run.Repository, stepRepo run.StepRepository) *Queue {
	return &Queue{
		runs:     make(map[string]*runEntry),
		runRepo:  runRepo,
		stepRepo: stepRepo,
	}
}

// SetDispatcher registers an external execution environment such as a K8s Job launcher.
func (q *Queue) SetDispatcher(d proto.Dispatcher) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dispatcher = d
}

// MakeTaskID creates a task ID from runID and stepName.
func MakeTaskID(runID, stepName string) string {
	return runID + ":" + stepName
}

// SplitTaskID splits a task ID into runID and stepName.
func SplitTaskID(id string) (runID, stepName string, err error) {
	idx := strings.Index(id, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid task id %q: missing colon separator", id)
	}
	return id[:idx], id[idx+1:], nil
}

// Add registers a pipeline in the queue and immediately marks steps with no dependencies as ready.
func (q *Queue) Add(ctx context.Context, pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string, vars proto.BuiltinVars, runParams map[string]any) {
	q.mu.Lock()
	defer q.mu.Unlock()

	pipelineJSON, _ := json.Marshal(pl)

	r := &runEntry{
		runID:   runID,
		pl:      pl,
		dag:     dag,
		tasks:   make(map[string]*taskEntry),
		addedAt: time.Now(),
	}

	for i := range pl.Spec.Steps {
		s := pl.Spec.Steps[i] // copy to avoid sharing pointer into the slice
		stepJSON, err := json.Marshal(&s)
		if err != nil {
			slog.Error("queue: marshal step failed", "run_id", runID, "step", s.Name, "err", err)
			continue
		}
		task := &proto.Task{
			ID:        MakeTaskID(runID, s.Name),
			RunID:     runID,
			StepName:  s.Name,
			Step:      stepJSON,
			Pipeline:  pipelineJSON,
			WorkDir:   workDir,
			OutputDir: outputDir,
			CreatedAt: time.Now(),
			Label:     s.Runner.Label,
			Vars:      vars,
			RunParams: runParams,
		}
		sCopy := s
		r.tasks[s.Name] = &taskEntry{task: task, step: &sCopy, status: taskPending}
	}

	q.promoteReady(ctx, r)
	q.runs[runID] = r
}

// Next transitions a ready task matching the given label to running and returns it. Returns nil if none available.
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
			slog.Info("task dispatched", "task_id", entry.task.ID, "label", label)
			return entry.task
		}
	}
	return nil
}

// Complete records the task result and processes downstream steps.
func (q *Queue) Complete(ctx context.Context, id, status, errMsg string, startedAt, endedAt time.Time, attempts int) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	runID, stepName, err := SplitTaskID(id)
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

	// Persist step result to DB
	now := endedAt
	if err := q.stepRepo.Upsert(ctx, &run.Step{
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
		q.promoteReady(ctx, r)
	} else {
		q.skipDownstream(ctx, r, stepName)
	}

	// If all steps are in a terminal state, the run is complete
	if q.allTerminal(r) {
		runStatus := run.StatusSuccess
		for _, e := range r.tasks {
			if e.status == taskFailed {
				runStatus = run.StatusFailed
				break
			}
		}
		finishedAt := time.Now()
		if err := q.runRepo.UpdateStatus(ctx, runID, runStatus, &finishedAt); err != nil {
			slog.Warn("update run status failed", "run_id", runID, "err", err)
		}
		pl := r.pl
		delete(q.runs, runID)
		slog.Info("run completed", "run_id", runID, "status", runStatus)

		if runStatus == run.StatusSuccess && q.OnRunSuccess != nil {
			go q.OnRunSuccess(ctx, runID, pl)
		}
	}

	return nil
}

func (q *Queue) promoteReady(ctx context.Context, r *runEntry) {
	done := q.doneNames(r)
	for _, entry := range r.tasks {
		if entry.status != taskPending {
			continue
		}
		if depsAllDone(entry.step.DependsOn, done) {
			entry.status = taskReady
			slog.Info("task ready", "task_id", entry.task.ID)
			q.dispatchIfNeeded(ctx, entry)
		}
	}
}

// dispatchIfNeeded immediately dispatches a task if a Dispatcher is configured.
// Called while holding the lock; captures the dispatcher reference before launching
// the goroutine to avoid a race with SetDispatcher.
func (q *Queue) dispatchIfNeeded(ctx context.Context, entry *taskEntry) {
	d := q.dispatcher // capture while holding the lock
	if d == nil {
		return
	}
	entry.status = taskRunning
	task := entry.task
	go func() {
		if err := d.Dispatch(ctx, task); err != nil {
			slog.Error("dispatch failed", "task_id", task.ID, "err", err)
			_ = q.Complete(ctx, task.ID, proto.TaskStatusFailed, err.Error(), time.Now(), time.Now(), 0)
		}
	}()
}

func (q *Queue) skipDownstream(ctx context.Context, r *runEntry, failedStep string) {
	for _, entry := range r.tasks {
		if entry.status != taskPending && entry.status != taskReady {
			continue
		}
		for _, dep := range entry.step.DependsOn {
			if dep == failedStep {
				entry.status = taskSkipped
				if err := q.stepRepo.Upsert(ctx, &run.Step{
					RunID:    r.runID,
					StepName: entry.step.Name,
					Status:   "skipped",
				}); err != nil {
					slog.Warn("upsert skipped step failed", "task_id", entry.task.ID, "err", err)
				}
				slog.Info("task skipped", "task_id", entry.task.ID, "failed_dep", failedStep)
				q.skipDownstream(ctx, r, entry.step.Name)
				break
			}
		}
	}
}

func (q *Queue) doneNames(r *runEntry) map[string]bool {
	done := make(map[string]bool)
	for name, entry := range r.tasks {
		if entry.status == taskDone || entry.status == taskSkipped {
			done[name] = true
		}
	}
	return done
}

func (q *Queue) allTerminal(r *runEntry) bool {
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

// Cleanup removes runs that have been stuck in the queue longer than ttl without reaching a terminal state.
// This guards against orphaned runs (e.g. a K8s job that never reports back).
func (q *Queue) Cleanup(ttl time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-ttl)
	for runID, r := range q.runs {
		if r.addedAt.Before(cutoff) {
			slog.Warn("queue: removing stuck run", "run_id", runID, "age", time.Since(r.addedAt).Round(time.Second))
			delete(q.runs, runID)
		}
	}
}
