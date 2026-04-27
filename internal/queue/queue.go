package queue

// Internal task queue — DAG-aware, for distributed worker execution.
// Task ID: "{runID}:{stepName}" (colon separator; step names must not contain a colon).
//
// When an ExecutionBackend is configured, Dispatch is called immediately when a task becomes ready.
// (Active dispatch mode, e.g. K8s Jobs)
// When backend is nil, workers pull tasks by polling /api/tasks/next.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/piper/piper/pkg/backend"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/run"
)

type taskStatus string

const (
	taskPending  taskStatus = "pending"
	taskReady    taskStatus = "ready"
	taskRunning  taskStatus = "running"
	taskRetrying taskStatus = "retrying"
	// taskDone and taskFailed use proto constants to stay in sync with worker/agent reporting.
	taskDone    taskStatus = proto.TaskStatusDone
	taskFailed  taskStatus = proto.TaskStatusFailed
	taskSkipped taskStatus = "skipped"
)

type taskEntry struct {
	task        *proto.Task
	step        *pipeline.Step
	status      taskStatus
	attempts    int
	maxAttempts int
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
	backend      backend.ExecutionBackend // nil means polling mode
	maxAttempts  int                      // total attempts, including the first try
	retryDelay   time.Duration
	OnRunSuccess func(ctx context.Context, runID string, pl *pipeline.Pipeline) // called (async) when a run succeeds
}

// NewQueue creates a new Queue backed by the given repositories.
func NewQueue(runRepo run.Repository, stepRepo run.StepRepository) *Queue {
	return &Queue{
		runs:        make(map[string]*runEntry),
		runRepo:     runRepo,
		stepRepo:    stepRepo,
		maxAttempts: 1,
	}
}

// SetBackend registers an external execution environment such as a K8s Job launcher.
func (q *Queue) SetBackend(b backend.ExecutionBackend) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.backend = b
}

// SetRetryPolicy configures queue-owned retries for distributed execution.
// maxAttempts is the total number of tries, including the first attempt.
func (q *Queue) SetRetryPolicy(maxAttempts int, retryDelay time.Duration) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.maxAttempts = maxAttempts
	q.retryDelay = retryDelay
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
		r.tasks[s.Name] = &taskEntry{task: task, step: &sCopy, status: taskPending, maxAttempts: q.maxAttempts}
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
			if entry.task.Label != "" && entry.task.Label != label {
				continue
			}
			q.startTaskLocked(context.Background(), r.runID, entry)
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

	now := endedAt
	if entry.attempts == 0 {
		entry.attempts = attempts
		if entry.attempts < 1 {
			entry.attempts = 1
		}
	}
	slog.Info("task completed", "task_id", id, "status", status)

	if status == string(taskDone) {
		entry.status = taskDone
		if err := q.stepRepo.Upsert(ctx, &run.Step{
			RunID:     runID,
			StepName:  stepName,
			Status:    status,
			StartedAt: &startedAt,
			EndedAt:   &now,
			Attempts:  entry.attempts,
			Error:     errMsg,
		}); err != nil {
			slog.Warn("upsert step failed", "task_id", id, "err", err)
		}
		q.promoteReady(ctx, r)
	} else if entry.attempts < entry.maxAttempts {
		entry.status = taskRetrying
		if err := q.stepRepo.Upsert(ctx, &run.Step{
			RunID:     runID,
			StepName:  stepName,
			Status:    string(taskRunning),
			StartedAt: &startedAt,
			EndedAt:   &now,
			Attempts:  entry.attempts,
			Error:     errMsg,
		}); err != nil {
			slog.Warn("upsert retry step failed", "task_id", id, "err", err)
		}
		q.scheduleRetryLocked(ctx, entry)
	} else {
		entry.status = taskFailed
		if err := q.stepRepo.Upsert(ctx, &run.Step{
			RunID:     runID,
			StepName:  stepName,
			Status:    status,
			StartedAt: &startedAt,
			EndedAt:   &now,
			Attempts:  entry.attempts,
			Error:     errMsg,
		}); err != nil {
			slog.Warn("upsert step failed", "task_id", id, "err", err)
		}
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

func (q *Queue) startTaskLocked(ctx context.Context, runID string, entry *taskEntry) {
	entry.attempts++
	entry.task.Attempt = entry.attempts
	entry.status = taskRunning
	now := time.Now()
	if err := q.stepRepo.Upsert(ctx, &run.Step{
		RunID:     runID,
		StepName:  entry.step.Name,
		Status:    string(taskRunning),
		StartedAt: &now,
		Attempts:  entry.attempts,
	}); err != nil {
		slog.Warn("upsert running step failed", "task_id", entry.task.ID, "err", err)
	}
}

// dispatchIfNeeded immediately dispatches a task if an ExecutionBackend is configured.
// Called while holding the lock; captures the backend reference before launching
// the goroutine to avoid a race with SetBackend.
func (q *Queue) dispatchIfNeeded(ctx context.Context, entry *taskEntry) {
	b := q.backend // capture while holding the lock
	if b == nil {
		return
	}
	runID := entry.task.RunID
	q.startTaskLocked(ctx, runID, entry)
	task := entry.task
	go func() {
		if err := b.Dispatch(ctx, task); err != nil {
			slog.Error("dispatch failed", "task_id", task.ID, "err", err)
			_ = q.Complete(ctx, task.ID, proto.TaskStatusFailed, err.Error(), time.Now(), time.Now(), 0)
		}
	}()
}

func (q *Queue) scheduleRetryLocked(ctx context.Context, entry *taskEntry) {
	if q.retryDelay <= 0 {
		entry.status = taskReady
		slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
		q.dispatchIfNeeded(ctx, entry)
		return
	}
	retry := func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if entry.status != taskRetrying {
			return
		}
		entry.status = taskReady
		slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
		q.dispatchIfNeeded(ctx, entry)
	}
	time.AfterFunc(q.retryDelay, retry)
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
