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
	// taskRecovering is a transient post-restart state for a step that was
	// running when the server crashed: it waits up to Queue.recoveryGrace for
	// the owning worker to reconnect and renew its lease before falling back
	// to failOrRetryLocked, instead of being re-dispatched immediately.
	taskRecovering taskStatus = "recovering"
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
	// deadline is the absolute time by which a running task must complete,
	// derived from step.options.timeout. Nil means unlimited.
	deadline *time.Time
	// timer is the single per-entry timer slot: retry-delay, timeout-deadline,
	// and recovery-grace are mutually exclusive (an entry is only ever in one
	// of those states at a time), so one slot is enough.
	timer *time.Timer
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
	mu            sync.Mutex
	runs          map[string]*runEntry // runID → entry
	runRepo       run.Repository
	stepRepo      run.StepRepository
	backend       pipelinedispatch.ExecutionBackend // nil means dispatch is disabled
	serverCtx     context.Context                   // cancelled on server shutdown; used for backend dispatch
	maxAttempts   int                               // total attempts, including the first try
	retryDelay    time.Duration
	recoveryGrace time.Duration // how long a recovered "running" step waits for the worker to reconnect
	storageURL    string
	storageToken  string
	OnRunSuccess  func(ctx context.Context, runID string, pl *pipeline.Pipeline) // called (async) when a run succeeds
	events        event.Publisher
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

// defaultRecoveryGrace is used when SetRecoveryGracePeriod is never called
// (or called with d<=0). It must comfortably exceed the worker's lease
// renewal interval (10s, see leaseLoop) so a single missed tick doesn't
// spuriously fail an otherwise-healthy recovered run.
const defaultRecoveryGrace = 45 * time.Second

// SetRecoveryGracePeriod configures how long a step that was "running" when
// the server crashed waits, after restart, for its owning worker to
// reconnect and renew its lease before failOrRetryLocked runs. d<=0 resets
// to defaultRecoveryGrace.
func (q *Queue) SetRecoveryGracePeriod(d time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recoveryGrace = d
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
	Attempts  int       // meaningful only when !Done; historical attempt count before the crash
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
				// Step was in-flight when the server crashed. Rather than
				// re-dispatching immediately (which risks a duplicate
				// side-effecting workload racing the original, still-running
				// one), wait up to recoveryGrace for the owning worker to
				// reconnect and renew its lease — see RenewLeases and
				// scheduleRecoveryGraceLocked.
				entry.status = taskRecovering
				entry.attempts = rs.Attempts
				startedAt := rs.StartedAt
				entry.startedAt = &startedAt
				now := time.Now()
				entry.leaseAt = &now
				q.emit(projectID, "step.recovering", map[string]any{"run_id": runID, "step": s.Name})
				q.scheduleRecoveryGraceLocked(runID, entry)
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
		q.stopEntryTimerLocked(entry)
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
		q.finalizeRunIfAllTerminalLocked(ctx, r)
	} else {
		q.failOrRetryLocked(ctx, r, entry, result.Error, &result.StartedAt, endedAt)
	}

	return nil
}

// failOrRetryLocked marks entry retrying (if attempts remain) or terminally
// failed (skipping downstream steps and finalizing the run if that was the
// last non-terminal step). It mirrors Complete()'s non-done branch so
// callers outside a real Complete() call — step timeout expiry, recovery
// grace expiry — get identical retry/finalize semantics instead of
// duplicating this decision. Called with q.mu held.
func (q *Queue) failOrRetryLocked(ctx context.Context, r *runEntry, entry *taskEntry, errMsg string, startedAt *time.Time, endedAt time.Time) {
	runID := r.runID
	stepName := entry.step.Name

	if entry.attempts < entry.maxAttempts {
		q.stopEntryTimerLocked(entry)
		entry.status = taskRetrying
		entry.assignedWorkerID = ""
		entry.startedAt = nil
		entry.leaseAt = nil
		if err := q.stepRepo.Upsert(ctx, &run.Step{
			ProjectID: r.projectID,
			RunID:     runID,
			StepName:  stepName,
			Status:    string(taskRunning),
			StartedAt: startedAt,
			EndedAt:   &endedAt,
			Attempts:  entry.attempts,
			Error:     errMsg,
		}); err != nil {
			slog.Warn("upsert retry step failed", "task_id", entry.task.ID, "err", err)
		}
		q.scheduleRetryLocked(ctx, entry)
		return
	}

	q.stopEntryTimerLocked(entry)
	entry.status = taskFailed
	entry.startedAt = nil
	entry.leaseAt = nil
	q.emit(r.projectID, "step.failed", map[string]any{"run_id": runID, "step": stepName, "attempts": entry.attempts, "error": errMsg})
	if err := q.stepRepo.Upsert(ctx, &run.Step{
		ProjectID: r.projectID,
		RunID:     runID,
		StepName:  stepName,
		Status:    string(taskFailed),
		StartedAt: startedAt,
		EndedAt:   &endedAt,
		Attempts:  entry.attempts,
		Error:     errMsg,
	}); err != nil {
		slog.Warn("upsert step failed", "task_id", entry.task.ID, "err", err)
	}
	q.skipDownstream(ctx, r, stepName)
	q.finalizeRunIfAllTerminalLocked(ctx, r)
}

// finalizeRunIfAllTerminalLocked marks the run success/failed and removes it
// from the in-memory queue once every step has reached a terminal state.
// Called with q.mu held.
func (q *Queue) finalizeRunIfAllTerminalLocked(ctx context.Context, r *runEntry) {
	if !q.allTerminal(r) {
		return
	}
	runID := r.runID
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
		// HTTP request context that triggered this call ends.
		q.OnRunSuccess(project.WithContext(context.Background(), project.Context{ID: r.projectID}), runID, pl)
	}
}

// Cancel stops queue-owned work for a run and marks any non-terminal steps as canceled.
// Active backends may also receive a cancellation request to stop in-flight work.
func (q *Queue) Cancel(ctx context.Context, projectID, runID string) error {
	q.mu.Lock()
	r, ok := q.runs[runID]
	b := q.backend
	owner, hasOwner := b.(pipelinedispatch.TaskOwner)
	if ok {
		projectID = r.projectID
		for _, entry := range r.tasks {
			switch entry.status {
			case taskDone, taskFailed, taskSkipped, taskCanceled:
				continue
			default:
				q.stopEntryTimerLocked(entry)
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
				// A dispatched-but-never-Complete()'d task (the normal case
				// for a cancel: the worker stops it without reporting a
				// result) would otherwise never release its router-level
				// capacity reservation, permanently starving future
				// dispatches to this agent. Safe to call unconditionally —
				// ReleaseTask no-ops for tasks that were never dispatched.
				if hasOwner {
					owner.ReleaseTask(entry.task.ID)
				}
			}
		}
		delete(q.runs, runID)
	}
	q.mu.Unlock()

	// Commit the local terminal state unconditionally: Piper's cancel contract
	// is "stop dispatch/retry and confirm locally," not "remote stop succeeded."
	// The remote call below is best-effort observability, not a precondition.
	now := time.Now()
	if err := q.runRepo.UpdateStatus(ctx, projectID, runID, run.StatusCanceled, &now); err != nil {
		return err
	}
	q.emit(projectID, "run.canceled", map[string]any{"run_id": runID})

	if cb, ok := b.(pipelinedispatch.CancelableBackend); ok {
		if err := cb.CancelRun(ctx, runID); err != nil {
			slog.Warn("remote cancel best-effort failed", "run_id", runID, "err", err)
			q.emit(projectID, "run.cancel_remote_failed", map[string]any{"run_id": runID, "err": err.Error()})
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
	entry.deadline = nil
	entry.task.Deadline = nil
	if timeout := entry.step.Options.Timeout; timeout > 0 {
		deadline := now.Add(time.Duration(timeout) * time.Second)
		entry.deadline = &deadline
		entry.task.Deadline = &deadline
		q.scheduleTimeoutLocked(runID, entry, deadline)
	}
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

// scheduleTimeoutLocked arms entry.timer to fail (or retry) the step once
// its declared step.options.timeout deadline passes. Late real results are
// unaffected: Complete()'s terminal-idempotency check silently ignores a
// success that arrives after the step has already been marked failed here.
// Called with q.mu held.
func (q *Queue) scheduleTimeoutLocked(runID string, entry *taskEntry, deadline time.Time) {
	q.stopEntryTimerLocked(entry)
	entry.timer = time.AfterFunc(time.Until(deadline), func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.serverCtx.Err() != nil || entry.status != taskRunning {
			return
		}
		r, ok := q.runs[runID]
		if !ok {
			return
		}
		entry.timer = nil
		// Unlike Complete(), this path never goes through a result, so it
		// must release the router-level capacity reservation itself —
		// otherwise the agent's reserved-slot count leaks by one on every
		// timeout, eventually starving all future dispatches to it even
		// though nothing is actually running.
		if owner, ok := q.backend.(pipelinedispatch.TaskOwner); ok {
			owner.ReleaseTask(entry.task.ID)
		}
		// Use a fresh context, not the caller's: the ctx that originally
		// scheduled this timer (an HTTP request or dispatch-result context)
		// is long since canceled by the time this fires, which would make
		// the DB writes below fail immediately (matches the established
		// pattern in scheduleRetryLocked/requeue below).
		q.failOrRetryLocked(context.Background(), r, entry, "task execution timeout", entry.startedAt, time.Now())
	})
}

// scheduleRecoveryGraceLocked arms entry.timer to fail (or retry, if a
// retry policy is configured) a step that was "running" when the server
// crashed, unless the owning worker reconnects and renews its lease for it
// within the grace period (see RenewLeases, which promotes a matching
// taskRecovering entry back to taskRunning and stops this timer). Called
// with q.mu held.
func (q *Queue) scheduleRecoveryGraceLocked(runID string, entry *taskEntry) {
	grace := q.recoveryGrace
	if grace <= 0 {
		grace = defaultRecoveryGrace
	}
	q.stopEntryTimerLocked(entry)
	entry.timer = time.AfterFunc(grace, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.serverCtx.Err() != nil || entry.status != taskRecovering {
			return
		}
		r, ok := q.runs[runID]
		if !ok {
			return
		}
		entry.timer = nil
		// See scheduleTimeoutLocked: release the router-level reservation
		// ourselves (no Complete() result on this path) and use a fresh
		// context (the caller's is long dead by the time this fires).
		if owner, ok := q.backend.(pipelinedispatch.TaskOwner); ok {
			owner.ReleaseTask(entry.task.ID)
		}
		q.failOrRetryLocked(context.Background(), r, entry, "recovery grace period expired without worker reconnect", entry.startedAt, time.Now())
	})
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
		if entry == nil || (entry.status != taskRunning && entry.status != taskRecovering) {
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
		if entry.status == taskRecovering {
			// The owning worker reconnected and is still executing this task:
			// stop waiting out the grace period and resume normal running state.
			q.stopEntryTimerLocked(entry)
			entry.status = taskRunning
			q.emit(r.projectID, "step.recovered", map[string]any{"run_id": runID, "step": stepName})
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
	// Copy rather than alias entry.task: the goroutine below reads task
	// fields without holding q.mu, and entry.task is mutated in place by a
	// later startTaskLocked call if this same entry gets retried (e.g. after
	// a fast timeout/failure with zero retry delay) before this goroutine's
	// Dispatch call has finished reading it.
	taskCopy := *entry.task
	task := &taskCopy
	dispatchCtx := q.serverCtx
	go func() {
		// entry may have been canceled between this goroutine's scheduling and
		// its run (Cancel() removes the run from q.runs but the entry pointer
		// stays valid). Re-check right before the actual dispatch call to close
		// the window where a cancel arrives before any run-worker binding exists.
		q.mu.Lock()
		canceled := entry.status == taskCanceled
		q.mu.Unlock()
		if canceled {
			return
		}
		if err := b.Dispatch(dispatchCtx, task); err != nil {
			var de *pipelinedispatch.DispatchError
			if errors.As(err, &de) && de.Retryable {
				q.mu.Lock()
				q.requeueBusyLocked(task.RunID, task.StepName, err)
				q.mu.Unlock()
				return
			}
			slog.Error("dispatch failed", "task_id", task.ID, "err", err)
			now := time.Now()
			workerID := task.WorkerID
			if owner, ok := b.(pipelinedispatch.TaskOwner); ok {
				if selectedWorkerID := owner.OwnerForTask(task.ID); selectedWorkerID != "" {
					workerID = selectedWorkerID
				}
			}
			if completeErr := q.Complete(dispatchCtx, proto.TaskResult{
				TaskID:    task.ID,
				WorkerID:  workerID,
				Status:    proto.TaskStatusFailed,
				Error:     err.Error(),
				StartedAt: now,
				EndedAt:   now,
				Attempt:   task.Attempt,
			}); completeErr != nil {
				slog.Error("record dispatch failure", "task_id", task.ID, "err", completeErr)
			}
		}
	}()
}

// requeueBusyLocked undoes startTaskLocked and puts the task back to ready
// without consuming a retry attempt. Called when dispatch returns a retryable error
// (for example, worker busy or no matching worker currently connected).
// Re-dispatches after a short fixed delay.
func (q *Queue) requeueBusyLocked(runID, stepName string, reason error) {
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
	slog.Info("task requeued after retryable dispatch failure", "task_id", entry.task.ID, "err", reason)
	q.stopEntryTimerLocked(entry)
	entry.timer = time.AfterFunc(2*time.Second, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		entry.timer = nil
		// serverCtx is cancelled on shutdown; without this check a timer that
		// outlives Close() (e.g. mid-flight when the process/test tears down)
		// would dispatch against an already-closed store.
		if q.serverCtx.Err() != nil || entry.status != taskReady {
			return
		}
		q.dispatchIfNeeded(context.Background(), entry)
	})
}

func (q *Queue) scheduleRetryLocked(ctx context.Context, entry *taskEntry) {
	if q.retryDelay <= 0 {
		entry.status = taskReady
		entry.timer = nil
		slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
		q.dispatchIfNeeded(ctx, entry)
		return
	}
	q.stopEntryTimerLocked(entry)
	retry := func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if entry.status != taskRetrying {
			return
		}
		entry.timer = nil
		entry.status = taskReady
		slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
		q.dispatchIfNeeded(context.Background(), entry)
	}
	entry.timer = time.AfterFunc(q.retryDelay, retry)
}

func (q *Queue) stopEntryTimerLocked(entry *taskEntry) {
	if entry.timer == nil {
		return
	}
	entry.timer.Stop()
	entry.timer = nil
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
					q.stopEntryTimerLocked(entry)
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
		// taskRecovering is included as a backstop in case its own grace
		// timer is ever lost (e.g. a second server restart racing the
		// timer); the normal path is scheduleRecoveryGraceLocked's own
		// shorter timer, not this coarser TTL sweep.
		if (entry.status != taskRunning && entry.status != taskRecovering) || entry.leaseAt == nil {
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
