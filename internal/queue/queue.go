package queue

// Internal task queue — DAG-aware, for distributed worker execution.
// Task ID: "{runID}:{stepName}" (colon separator; step names must not contain a colon).
//
// When an ExecutionBackend is configured, Dispatch is called immediately when a task becomes ready.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/piper/piper/internal/event"
	"github.com/piper/piper/internal/pipelinedispatch"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/run"
	"github.com/piper/piper/pkg/project"
)

type taskStatus string

const (
	taskPending  taskStatus = "pending"
	taskReady    taskStatus = "ready"
	taskRunning  taskStatus = "running"
	taskRetrying taskStatus = "retrying"
	// taskDone and taskFailed use proto constants to stay in sync with worker/agent reporting.
	taskDone     taskStatus = proto.TaskStatusDone
	taskFailed   taskStatus = proto.TaskStatusFailed
	taskSkipped  taskStatus = "skipped"
	taskCanceled taskStatus = "canceled"
)

type taskEntry struct {
	task             *proto.Task
	step             *pipeline.Step
	status           taskStatus
	attempts         int
	maxAttempts      int
	assignedWorkerID string
	startedAt        *time.Time
	leaseAt          *time.Time
	retryTimer       *time.Timer
}

type runEntry struct {
	projectID string
	runID     string
	pl        *pipeline.Pipeline
	dag       *pipeline.DAG
	tasks     map[string]*taskEntry // stepName → entry
	addedAt   time.Time
}

// Queue is the DAG-aware task queue for distributed worker execution.
type Queue struct {
	mu           sync.Mutex
	runs         map[string]*runEntry // runID → entry
	runRepo      run.Repository
	stepRepo     run.StepRepository
	backend      pipelinedispatch.ExecutionBackend // nil means dispatch is disabled
	serverCtx    context.Context                   // cancelled on server shutdown; used for backend dispatch
	maxAttempts  int                               // total attempts, including the first try
	retryDelay   time.Duration
	storageURL   string
	storageToken string
	OnRunSuccess func(ctx context.Context, runID string, pl *pipeline.Pipeline) // called (async) when a run succeeds
	events       event.Publisher
}

// NewQueue creates a new Queue backed by the given repositories.
// serverCtx is the server's lifetime context: dispatch goroutines are tied to it
// so they are cancelled on shutdown but not on individual HTTP request completion.
func NewQueue(serverCtx context.Context, runRepo run.Repository, stepRepo run.StepRepository) *Queue {
	return &Queue{
		runs:        make(map[string]*runEntry),
		runRepo:     runRepo,
		stepRepo:    stepRepo,
		serverCtx:   serverCtx,
		maxAttempts: 1,
	}
}

// SetEventPublisher wires an event.Publisher so the queue can emit structured events.
func (q *Queue) SetEventPublisher(p event.Publisher) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = p
}

// SetBackend registers an external execution environment such as a K8s Job launcher.
func (q *Queue) SetBackend(b pipelinedispatch.ExecutionBackend) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.backend = b
}

// SetStorageConfig installs the master-owned effective artifact/source storage
// settings copied into every newly-created task.
func (q *Queue) SetStorageConfig(storageURL, storageToken string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.storageURL = storageURL
	q.storageToken = storageToken
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
func (q *Queue) Add(ctx context.Context, projectID string, pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string, vars proto.BuiltinVars, runParams map[string]any) {
	q.AddWithEnv(ctx, projectID, pl, dag, runID, workDir, outputDir, vars, runParams, nil)
}

func (q *Queue) AddWithEnv(ctx context.Context, projectID string, pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string, vars proto.BuiltinVars, runParams map[string]any, envByStep map[string][]string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	pl = pl.ApplyDefaults()
	pipelineJSON, _ := json.Marshal(pl)

	r := &runEntry{
		projectID: projectID,
		runID:     runID,
		pl:        pl,
		dag:       dag,
		tasks:     make(map[string]*taskEntry),
		addedAt:   time.Now(),
	}

	for i := range pl.Spec.Steps {
		s := pl.Spec.Steps[i] // copy to avoid sharing pointer into the slice
		stepJSON, err := json.Marshal(&s)
		if err != nil {
			slog.Error("queue: marshal step failed", "run_id", runID, "step", s.Name, "err", err)
			continue
		}
		task := &proto.Task{
			ProjectID: projectID,
			ID:        MakeTaskID(runID, s.Name),
			RunID:     runID,
			StepName:  s.Name,
			Step:      stepJSON,
			Pipeline:  pipelineJSON,
			WorkDir:   workDir,
			OutputDir: outputDir,
			CreatedAt: time.Now(),
			Label:     s.Driver.Placement.Label,
			WorkerID: func() string {
				if pl.Spec.Defaults != nil {
					return pl.Spec.Defaults.Driver.Placement.Worker
				}
				return ""
			}(),
			Vars:      vars,
			RunParams: runParams,
			Env:       append([]string{}, envByStep[s.Name]...),

			StorageURL:   q.storageURL,
			StorageToken: q.storageToken,
		}
		sCopy := s
		r.tasks[s.Name] = &taskEntry{task: task, step: &sCopy, status: taskPending, maxAttempts: q.maxAttempts}
	}

	q.promoteReady(ctx, r)
	q.runs[runID] = r
}

// RecoveredStep describes the persisted state of a step from a previous server session.
// Done == true means the step finished (done or skipped).
// Done == false with a non-zero StartedAt means the step was running when the server crashed.
// Steps absent from the slice are treated as pending.
type RecoveredStep struct {
	Name      string
	Done      bool
	StartedAt time.Time // meaningful only when !Done
}

// Recover re-adds an interrupted run from a previous server session.
// recovered lists every step whose state was persisted; absent steps are treated as pending.
func (q *Queue) Recover(ctx context.Context, projectID string, pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string, vars proto.BuiltinVars, runParams map[string]any, recovered []RecoveredStep) {
	q.RecoverWithEnv(ctx, projectID, pl, dag, runID, workDir, outputDir, vars, runParams, recovered, nil)
}

func (q *Queue) RecoverWithEnv(ctx context.Context, projectID string, pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string, vars proto.BuiltinVars, runParams map[string]any, recovered []RecoveredStep, envByStep map[string][]string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	pl = pl.ApplyDefaults()
	recoveredByName := make(map[string]RecoveredStep, len(recovered))
	for _, rs := range recovered {
		recoveredByName[rs.Name] = rs
	}

	pipelineJSON, _ := json.Marshal(pl)
	r := &runEntry{
		projectID: projectID,
		runID:     runID,
		pl:        pl,
		dag:       dag,
		tasks:     make(map[string]*taskEntry),
		addedAt:   time.Now(),
	}

	for i := range pl.Spec.Steps {
		s := pl.Spec.Steps[i]
		stepJSON, _ := json.Marshal(&s)
		task := &proto.Task{
			ProjectID: projectID,
			ID:        MakeTaskID(runID, s.Name),
			RunID:     runID,
			StepName:  s.Name,
			Step:      stepJSON,
			Pipeline:  pipelineJSON,
			WorkDir:   workDir,
			OutputDir: outputDir,
			CreatedAt: time.Now(),
			Label:     s.Driver.Placement.Label,
			WorkerID: func() string {
				if pl.Spec.Defaults != nil {
					return pl.Spec.Defaults.Driver.Placement.Worker
				}
				return ""
			}(),
			Vars:      vars,
			RunParams: runParams,
			Env:       append([]string{}, envByStep[s.Name]...),

			StorageURL:   q.storageURL,
			StorageToken: q.storageToken,
		}
		sCopy := s
		entry := &taskEntry{task: task, step: &sCopy, status: taskPending, maxAttempts: q.maxAttempts}
		if rs, ok := recoveredByName[s.Name]; ok {
			if rs.Done {
				entry.status = taskDone
			} else if !rs.StartedAt.IsZero() {
				// Step was in-flight when the server crashed. Reset to pending so it
				// gets re-dispatched immediately. For K8s this may cause at-most-once
				// duplication (old pod + new dispatch), but the stale result is rejected
				// by the worker-ownership check in Complete(). For embedded/bare-metal
				// workers the previous process is gone and re-dispatch is required.
				entry.status = taskPending
			}
		}
		r.tasks[s.Name] = entry
	}

	if q.allTerminal(r) {
		// All steps already in terminal state — finalise the run without re-queuing.
		runStatus := run.StatusSuccess
		for _, e := range r.tasks {
			if e.status == taskFailed {
				runStatus = run.StatusFailed
				break
			}
		}
		finishedAt := time.Now()
		_ = q.runRepo.UpdateStatus(ctx, projectID, runID, runStatus, &finishedAt)
		return
	}

	q.promoteReady(ctx, r)
	q.runs[runID] = r
	slog.Info("run recovered into queue", "run_id", runID, "recovered_steps", len(recovered))
}

// Complete records the task result and processes downstream steps.
func (q *Queue) Complete(ctx context.Context, result proto.TaskResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	runID, stepName, err := SplitTaskID(result.TaskID)
	if err != nil {
		return err
	}

	r, ok := q.runs[runID]
	if !ok {
		// Run was removed from the in-memory queue (completed, failed, or canceled).
		// Treat any late result as idempotent rather than an error.
		return nil
	}
	entry, ok := r.tasks[stepName]
	if !ok {
		return fmt.Errorf("step %s not found in run %s", stepName, runID)
	}
	ownerID := entry.assignedWorkerID
	if ownerID == "" {
		ownerID = entry.task.WorkerID
	}
	if ownerID == "" {
		if owner, ok := q.backend.(pipelinedispatch.TaskOwner); ok {
			ownerID = owner.OwnerForTask(result.TaskID)
		}
	}
	if ownerID != "" && result.WorkerID == "" {
		return fmt.Errorf("task %s completion missing worker identity", result.TaskID)
	}
	if result.WorkerID != "" && ownerID != "" && result.WorkerID != ownerID {
		return fmt.Errorf("task %s owned by worker %q, result from %q rejected", result.TaskID, ownerID, result.WorkerID)
	}
	resultAttempt := result.Attempt
	if resultAttempt == 0 {
		resultAttempt = 1
	}

	// Idempotency: ignore duplicate result for an already-terminal step.
	switch entry.status {
	case taskDone, taskFailed, taskSkipped, taskCanceled:
		return nil
	}

	// Stale result: arrived late from a previous attempt.
	if entry.attempts > 0 && resultAttempt < entry.attempts {
		slog.Warn("stale task result ignored", "task_id", result.TaskID, "result_attempt", resultAttempt, "current_attempt", entry.attempts)
		return nil
	}

	// Future attempt: should never happen in normal flow.
	if resultAttempt > entry.attempts {
		return fmt.Errorf("task %s: result attempt %d exceeds current attempt %d", result.TaskID, resultAttempt, entry.attempts)
	}

	if owner, ok := q.backend.(pipelinedispatch.TaskOwner); ok {
		owner.ReleaseTask(result.TaskID)
	}

	if entry.attempts == 0 {
		entry.attempts = resultAttempt
	}

	endedAt := result.EndedAt
	q.emit(r.projectID, "step.reported", map[string]any{"run_id": runID, "step": stepName, "task_id": result.TaskID, "status": result.Status})

	if result.Status == string(taskDone) {
		q.stopRetryTimerLocked(entry)
		entry.status = taskDone
		entry.startedAt = nil
		entry.leaseAt = nil
		q.emit(r.projectID, "step.done", map[string]any{"run_id": runID, "step": stepName, "attempts": entry.attempts})
		if err := q.stepRepo.Upsert(ctx, &run.Step{
			ProjectID: r.projectID,
			RunID:     runID,
			StepName:  stepName,
			Status:    result.Status,
			StartedAt: &result.StartedAt,
			EndedAt:   &endedAt,
			Attempts:  entry.attempts,
			Error:     result.Error,
		}); err != nil {
			slog.Warn("upsert step failed", "task_id", result.TaskID, "err", err)
		}
		q.promoteReady(ctx, r)
	} else if entry.attempts < entry.maxAttempts {
		q.stopRetryTimerLocked(entry)
		entry.status = taskRetrying
		entry.assignedWorkerID = ""
		entry.startedAt = nil
		entry.leaseAt = nil
		if err := q.stepRepo.Upsert(ctx, &run.Step{
			ProjectID: r.projectID,
			RunID:     runID,
			StepName:  stepName,
			Status:    string(taskRunning),
			StartedAt: &result.StartedAt,
			EndedAt:   &endedAt,
			Attempts:  entry.attempts,
			Error:     result.Error,
		}); err != nil {
			slog.Warn("upsert retry step failed", "task_id", result.TaskID, "err", err)
		}
		q.scheduleRetryLocked(ctx, entry)
	} else {
		q.stopRetryTimerLocked(entry)
		entry.status = taskFailed
		entry.startedAt = nil
		entry.leaseAt = nil
		q.emit(r.projectID, "step.failed", map[string]any{"run_id": runID, "step": stepName, "attempts": entry.attempts, "error": result.Error})
		if err := q.stepRepo.Upsert(ctx, &run.Step{
			ProjectID: r.projectID,
			RunID:     runID,
			StepName:  stepName,
			Status:    result.Status,
			StartedAt: &result.StartedAt,
			EndedAt:   &endedAt,
			Attempts:  entry.attempts,
			Error:     result.Error,
		}); err != nil {
			slog.Warn("upsert step failed", "task_id", result.TaskID, "err", err)
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
		if err := q.runRepo.UpdateStatus(ctx, r.projectID, runID, runStatus, &finishedAt); err != nil {
			slog.Warn("update run status failed", "run_id", runID, "err", err)
		}
		pl := r.pl
		delete(q.runs, runID)
		if owner, ok := q.backend.(pipelinedispatch.RunOwner); ok {
			owner.ReleaseRun(runID)
		}
		q.emit(r.projectID, "run.completed", map[string]any{"run_id": runID, "status": runStatus})

		if runStatus == run.StatusSuccess && q.OnRunSuccess != nil {
			// Use a detached context so the callback isn't cancelled when the
			// HTTP request context that triggered Complete() ends.
			q.OnRunSuccess(project.WithContext(context.Background(), project.Context{ID: r.projectID}), runID, pl)
		}
	}

	return nil
}

// Cancel stops queue-owned work for a run and marks any non-terminal steps as canceled.
// Active backends may also receive a cancellation request to stop in-flight work.
func (q *Queue) Cancel(ctx context.Context, projectID, runID string) error {
	q.mu.Lock()
	r, ok := q.runs[runID]
	b := q.backend
	if ok {
		projectID = r.projectID
		for _, entry := range r.tasks {
			switch entry.status {
			case taskDone, taskFailed, taskSkipped, taskCanceled:
				continue
			default:
				q.stopRetryTimerLocked(entry)
				startedAt := entry.startedAt
				entry.status = taskCanceled
				entry.startedAt = nil
				now := time.Now()
				if err := q.stepRepo.Upsert(ctx, &run.Step{
					ProjectID: r.projectID,
					RunID:     runID,
					StepName:  entry.step.Name,
					Status:    string(taskCanceled),
					StartedAt: startedAt,
					EndedAt:   &now,
					Attempts:  entry.attempts,
				}); err != nil {
					slog.Warn("upsert canceled step failed", "task_id", entry.task.ID, "err", err)
				}
			}
		}
		delete(q.runs, runID)
	}
	q.mu.Unlock()

	if cb, ok := b.(pipelinedispatch.CancelableBackend); ok {
		if err := cb.CancelRun(ctx, runID); err != nil {
			return err
		}
	}

	now := time.Now()
	q.emit(projectID, "run.canceled", map[string]any{"run_id": runID})
	return q.runRepo.UpdateStatus(ctx, projectID, runID, run.StatusCanceled, &now)
}

func (q *Queue) promoteReady(ctx context.Context, r *runEntry) {
	done := q.doneNames(r)
	for _, entry := range r.tasks {
		if entry.status != taskPending {
			continue
		}
		if depsAllDone(entry.step.DependsOn, done) {
			entry.status = taskReady
			q.emit(r.projectID, "step.ready", map[string]any{"run_id": r.runID, "step": entry.step.Name, "task_id": entry.task.ID})
			q.dispatchIfNeeded(ctx, entry)
		}
	}
}

func (q *Queue) startTaskLocked(ctx context.Context, runID string, entry *taskEntry) {
	entry.attempts++
	entry.task.Attempt = entry.attempts
	entry.status = taskRunning
	now := time.Now()
	entry.startedAt = &now
	entry.leaseAt = &now
	q.emit(entry.task.ProjectID, "step.running", map[string]any{"run_id": runID, "step": entry.step.Name, "task_id": entry.task.ID, "attempt": entry.attempts})
	if err := q.stepRepo.Upsert(ctx, &run.Step{
		ProjectID: entry.task.ProjectID,
		RunID:     runID,
		StepName:  entry.step.Name,
		Status:    string(taskRunning),
		StartedAt: &now,
		Attempts:  entry.attempts,
	}); err != nil {
		slog.Warn("upsert running step failed", "task_id", entry.task.ID, "err", err)
	}
}

// RenewLeases records that workerID is still executing the given tasks.
// Worker liveness and task state remain separate: only explicit task IDs renew leases.
func (q *Queue) RenewLeases(workerID string, taskIDs []string) {
	if workerID == "" || len(taskIDs) == 0 {
		return
	}
	now := time.Now()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, taskID := range taskIDs {
		runID, stepName, err := SplitTaskID(taskID)
		if err != nil {
			continue
		}
		r := q.runs[runID]
		if r == nil {
			continue
		}
		entry := r.tasks[stepName]
		if entry == nil || entry.status != taskRunning {
			continue
		}
		if entry.assignedWorkerID == "" {
			if entry.task.WorkerID != "" && entry.task.WorkerID != workerID {
				continue
			}
			entry.assignedWorkerID = workerID
		}
		if entry.assignedWorkerID != workerID {
			continue
		}
		entry.leaseAt = &now
	}
}

func (q *Queue) emit(projectID, eventType string, fields map[string]any) {
	slog.Info("event", "type", eventType, "fields", fields)
	if q.events != nil {
		if projectID == "" {
			q.events.Publish(event.NewInfra(eventType, fields))
		} else {
			q.events.Publish(event.New(projectID, eventType, fields))
		}
	}
}

// dispatchIfNeeded immediately dispatches a task if an ExecutionBackend is configured.
// A nil backend leaves tasks ready until a backend is configured.
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
	dispatchCtx := q.serverCtx
	go func() {
		if err := b.Dispatch(dispatchCtx, task); err != nil {
			var de *pipelinedispatch.DispatchError
			if errors.As(err, &de) && de.Retryable {
				q.mu.Lock()
				q.requeueBusyLocked(task.RunID, task.StepName)
				q.mu.Unlock()
				return
			}
			slog.Error("dispatch failed", "task_id", task.ID, "err", err)
			now := time.Now()
			_ = q.Complete(dispatchCtx, proto.TaskResult{
				TaskID:    task.ID,
				Status:    proto.TaskStatusFailed,
				Error:     err.Error(),
				StartedAt: now,
				EndedAt:   now,
				Attempt:   task.Attempt,
			})
		}
	}()
}

// requeueBusyLocked undoes startTaskLocked and puts the task back to ready
// without consuming a retry attempt. Called when dispatch returns a retryable error
// (e.g. worker busy). Re-dispatches after a short fixed delay.
func (q *Queue) requeueBusyLocked(runID, stepName string) {
	r := q.runs[runID]
	if r == nil {
		return
	}
	entry := r.tasks[stepName]
	if entry == nil || entry.status != taskRunning {
		return
	}
	entry.attempts--
	if entry.attempts < 0 {
		entry.attempts = 0
	}
	entry.task.Attempt = entry.attempts
	entry.status = taskReady
	entry.assignedWorkerID = ""
	entry.startedAt = nil
	entry.leaseAt = nil
	slog.Info("task requeued after busy dispatch", "task_id", entry.task.ID)
	time.AfterFunc(2*time.Second, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if entry.status != taskReady {
			return
		}
		q.dispatchIfNeeded(context.Background(), entry)
	})
}

func (q *Queue) scheduleRetryLocked(ctx context.Context, entry *taskEntry) {
	if q.retryDelay <= 0 {
		entry.status = taskReady
		entry.retryTimer = nil
		slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
		q.dispatchIfNeeded(ctx, entry)
		return
	}
	q.stopRetryTimerLocked(entry)
	retry := func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if entry.status != taskRetrying {
			return
		}
		entry.retryTimer = nil
		entry.status = taskReady
		slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
		q.dispatchIfNeeded(context.Background(), entry)
	}
	entry.retryTimer = time.AfterFunc(q.retryDelay, retry)
}

func (q *Queue) stopRetryTimerLocked(entry *taskEntry) {
	if entry.retryTimer == nil {
		return
	}
	entry.retryTimer.Stop()
	entry.retryTimer = nil
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
					ProjectID: r.projectID,
					RunID:     r.runID,
					StepName:  entry.step.Name,
					Status:    "skipped",
				}); err != nil {
					slog.Warn("upsert skipped step failed", "task_id", entry.task.ID, "err", err)
				}
				q.emit(r.projectID, "step.skipped", map[string]any{"run_id": r.runID, "step": entry.step.Name, "task_id": entry.task.ID, "failed_dep": failedStep})
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

// Cleanup fails runs with actively running tasks older than ttl without reaching a terminal state.
// This guards against orphaned runs (e.g. a K8s job that never reports back).
func (q *Queue) Cleanup(ctx context.Context, ttl time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-ttl)
	for runID, r := range q.runs {
		if q.runExpiredLocked(r, cutoff) {
			now := time.Now()
			for _, entry := range r.tasks {
				switch entry.status {
				case taskDone, taskFailed, taskSkipped, taskCanceled:
					continue
				default:
					q.stopRetryTimerLocked(entry)
					entry.status = taskFailed
					entry.startedAt = nil
					entry.leaseAt = nil
					if err := q.stepRepo.Upsert(ctx, &run.Step{
						ProjectID: r.projectID,
						RunID:     runID,
						StepName:  entry.step.Name,
						Status:    string(taskFailed),
						EndedAt:   &now,
						Attempts:  entry.attempts,
						Error:     "task lease expired",
					}); err != nil {
						slog.Warn("upsert expired step failed", "task_id", entry.task.ID, "err", err)
					}
				}
			}
			if err := q.runRepo.UpdateStatus(ctx, r.projectID, runID, run.StatusFailed, &now); err != nil {
				slog.Warn("update expired run failed", "run_id", runID, "err", err)
			}
			q.emit(r.projectID, "run.expired", map[string]any{"run_id": runID, "status": run.StatusFailed})
			delete(q.runs, runID)
			if owner, ok := q.backend.(pipelinedispatch.RunOwner); ok {
				owner.ReleaseRun(runID)
			}
		}
	}
}

func (q *Queue) runExpiredLocked(r *runEntry, cutoff time.Time) bool {
	for _, entry := range r.tasks {
		if entry.status != taskRunning || entry.leaseAt == nil {
			continue
		}
		if entry.leaseAt.Before(cutoff) {
			return true
		}
	}
	return false
}

type Stats struct {
	Runs    int
	Pending int
	Ready   int
	Running int
}

func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	var s Stats
	s.Runs = len(q.runs)
	for _, r := range q.runs {
		for _, entry := range r.tasks {
			switch entry.status {
			case taskPending:
				s.Pending++
			case taskReady:
				s.Ready++
			case taskRunning:
				s.Running++
			}
		}
	}
	return s
}
