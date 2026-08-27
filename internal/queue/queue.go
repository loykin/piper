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

	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/internal/pipelinedispatch"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
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
	task              *proto.Task
	step              *pipeline.Step
	status            taskStatus
	attempts          int
	maxAttempts       int
	assignedRuntimeID string
	startedAt         *time.Time
	leaseAt           *time.Time
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
	OnRunOutcome  func(ctx context.Context, projectID, runID, status string, pl *pipeline.Pipeline)
	events        event.Publisher
	// pendingWrites accumulates repository writes decided by the current
	// locked call while q.mu is held (see appendWrite). Only ever touched
	// under q.mu, same as every other field on Queue — no separate
	// synchronization needed. Each top-level entry point (Complete, Cancel,
	// Cleanup, a fired timer, ...) drains it into a local slice and calls
	// flushPending *after* releasing q.mu, so the DB I/O itself never happens
	// while the lock is held. There is no owned goroutine or background
	// queue: the write runs synchronously, on whatever goroutine triggered
	// the state transition, right after that goroutine's own q.mu.Unlock().
	pendingWrites []pendingWrite
	// pendingEffects accumulates non-persistence side effects (event
	// emission, the OnRunSuccess callback) decided under the same lock — see
	// appendEffect. Drained and run together with pendingWrites by
	// flushPending, always *after* the writes, so a subscriber can never
	// observe a state-transition event before the DB row backing it is
	// durable.
	pendingEffects []func(ctx context.Context)
	// wg tracks every ephemeral goroutine the queue spawns on its own
	// initiative — the dispatch goroutine in dispatchIfNeeded and each armed
	// entry.timer's AfterFunc callback (timeout, retry, requeue, recovery
	// grace). Close blocks until this drains (or its context expires) so a
	// caller tearing down the server never closes the DB out from under a
	// write one of these goroutines is still in the middle of flushing.
	// Add(1) always happens under q.mu, at the same point the goroutine/timer
	// is created; Done() is called exactly once per Add — either by the
	// goroutine/callback itself, or by stopEntryTimerLocked when it manages
	// to cancel the timer before it fires (see the comment there).
	wg sync.WaitGroup
}

// Close waits for every in-flight queue-initiated goroutine (dispatch calls,
// armed timeout/retry/recovery-grace timers) to finish, bounded by ctx. Call
// this — with serverCtx (the context passed to NewQueue) still live — before
// cancelling that context or closing the repositories it writes to: a
// goroutine that's mid-flush needs both to still be alive to complete
// durably. Returns ctx.Err() if the deadline/cancellation won first.
func (q *Queue) Close(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

// pendingWrite is one repository write a state-transition function decided
// on while holding q.mu, deferred until after the lock is released.
type pendingWrite struct {
	desc string
	fn   func(ctx context.Context) error
}

// appendWrite records a repository write to run once the caller's top-level
// lock scope releases q.mu. Call this instead of calling stepRepo/runRepo
// directly from any Locked function — it's what keeps q.mu from being held
// across real DB I/O. Must be called with q.mu held.
func (q *Queue) appendWrite(desc string, fn func(ctx context.Context) error) {
	q.pendingWrites = append(q.pendingWrites, pendingWrite{desc: desc, fn: fn})
}

// appendEffect records a non-persistence side effect (event emission, the
// OnRunSuccess callback) to run once the caller's top-level lock scope
// releases q.mu, after pendingWrites have been flushed. Call this instead of
// calling q.emit or OnRunSuccess directly from any Locked function that also
// records a write for the same state transition — it's what keeps
// subscribers from observing an event before its DB row is durable. Must be
// called with q.mu held.
func (q *Queue) appendEffect(fn func(ctx context.Context)) {
	q.pendingEffects = append(q.pendingEffects, fn)
}

// pendingOutcome is the pair of deferred work a Locked function hands back
// to its public wrapper: writes to persist, and effects (events, callbacks)
// to run once those writes are done.
type pendingOutcome struct {
	writes  []pendingWrite
	effects []func(ctx context.Context)
}

// takePendingLocked detaches and returns the accumulated writes and effects
// so the caller can run them after unlocking. Must be called with q.mu held,
// as the last thing before unlock.
func (q *Queue) takePendingLocked() pendingOutcome {
	out := pendingOutcome{writes: q.pendingWrites, effects: q.pendingEffects}
	q.pendingWrites = nil
	q.pendingEffects = nil
	return out
}

// flushPending persists each accumulated write in order, retrying
// individually with bounded backoff, then runs the accumulated effects
// (event emission, OnRunSuccess) — always after the writes, never
// interleaved. Must be called without q.mu held — this is what actually
// performs the DB I/O and side effects that Locked functions only decided
// on.
func flushPending(ctx context.Context, out pendingOutcome) {
	for _, w := range out.writes {
		persistWithRetry(ctx, w.desc, w.fn)
	}
	for _, fn := range out.effects {
		fn(ctx)
	}
}

// persistWithRetry retries a single write with bounded exponential backoff.
// This is deliberately not "retry forever": giving up is logged at ERROR —
// loud enough to alert on — rather than silently moving on, but a write that
// keeps failing does not block anything else (there is no shared queue left
// for it to stall).
func persistWithRetry(ctx context.Context, desc string, fn func(ctx context.Context) error) {
	const maxAttempts = 5
	backoff := 100 * time.Millisecond
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := fn(ctx)
		if err == nil {
			return
		}
		if attempt == maxAttempts {
			slog.Error("queue: persist failed, giving up after retries", "op", desc, "attempts", attempt, "err", err)
			return
		}
		slog.Warn("queue: persist failed, retrying", "op", desc, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
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
	outcome := q.addWithEnvLocked(ctx, projectID, pl, dag, runID, workDir, outputDir, vars, runParams, envByStep)
	flushPending(ctx, outcome)
}

func (q *Queue) addWithEnvLocked(ctx context.Context, projectID string, pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string, vars proto.BuiltinVars, runParams map[string]any, envByStep map[string][]string) pendingOutcome {
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
	return q.takePendingLocked()
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
	outcome := q.recoverWithEnvLocked(ctx, projectID, pl, dag, runID, workDir, outputDir, vars, runParams, recovered, envByStep)
	flushPending(ctx, outcome)
}

func (q *Queue) recoverWithEnvLocked(ctx context.Context, projectID string, pl *pipeline.Pipeline, dag *pipeline.DAG, runID, workDir, outputDir string, vars proto.BuiltinVars, runParams map[string]any, recovered []RecoveredStep, envByStep map[string][]string) pendingOutcome {
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
				q.transitionTaskLocked(entry, taskDone)
			} else if !rs.StartedAt.IsZero() {
				// Step was in-flight when the server crashed. Rather than
				// re-dispatching immediately (which risks a duplicate
				// side-effecting workload racing the original, still-running
				// one), wait up to recoveryGrace for the owning worker to
				// reconnect and renew its lease — see RenewLeases and
				// scheduleRecoveryGraceLocked.
				q.transitionTaskLocked(entry, taskRecovering)
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
		// All steps already in terminal state — finalise the run without
		// re-queuing. Goes through the same finalizeRunLocked as every other
		// termination path now, so this recovered run also gets its
		// run.completed event and OnRunSuccess trigger (a real gap before
		// this consolidation: a run that finished between its last write and
		// the crash used to be silently finalized with no event at all) and
		// a ReleaseRun call (cleans up a stale router reservation this run
		// may still hold from before the crash).
		runStatus := run.StatusSuccess
		for _, e := range r.tasks {
			if e.status == taskFailed {
				runStatus = run.StatusFailed
				break
			}
		}
		q.finalizeRunLocked(r, runStatus, "run.completed")
		return q.takePendingLocked()
	}

	q.promoteReady(ctx, r)
	q.runs[runID] = r
	slog.Info("run recovered into queue", "run_id", runID, "recovered_steps", len(recovered))
	return q.takePendingLocked()
}

// Complete records the task result and processes downstream steps.
func (q *Queue) Complete(ctx context.Context, result proto.TaskResult) error {
	err, outcome := q.completeLocked(ctx, result)
	flushPending(ctx, outcome)
	return err
}

func (q *Queue) completeLocked(ctx context.Context, result proto.TaskResult) (error, pendingOutcome) {
	q.mu.Lock()
	defer q.mu.Unlock()

	runID, stepName, err := SplitTaskID(result.TaskID)
	if err != nil {
		return err, q.takePendingLocked()
	}

	r, ok := q.runs[runID]
	if !ok {
		// Run was removed from the in-memory queue (completed, failed, or canceled).
		// Treat any late result as idempotent rather than an error.
		return nil, q.takePendingLocked()
	}
	entry, ok := r.tasks[stepName]
	if !ok {
		return fmt.Errorf("step %s not found in run %s", stepName, runID), q.takePendingLocked()
	}
	ownerID := entry.assignedRuntimeID
	if ownerID == "" {
		ownerID = entry.task.RuntimeID
	}
	if ownerID == "" {
		if owner, ok := q.backend.(pipelinedispatch.TaskOwner); ok {
			ownerID = owner.OwnerForTask(result.TaskID)
		}
	}
	if ownerID != "" && result.RuntimeID == "" {
		return fmt.Errorf("task %s completion missing runtime identity", result.TaskID), q.takePendingLocked()
	}
	if result.RuntimeID != "" && ownerID != "" && result.RuntimeID != ownerID {
		return fmt.Errorf("task %s owned by runtime %q, result from %q rejected", result.TaskID, ownerID, result.RuntimeID), q.takePendingLocked()
	}
	resultAttempt := result.Attempt
	if resultAttempt == 0 {
		resultAttempt = 1
	}

	// Idempotency: ignore duplicate result for an already-terminal step.
	switch entry.status {
	case taskDone, taskFailed, taskSkipped, taskCanceled:
		return nil, q.takePendingLocked()
	}

	// Stale result: arrived late from a previous attempt.
	if entry.attempts > 0 && resultAttempt < entry.attempts {
		slog.Warn("stale task result ignored", "task_id", result.TaskID, "result_attempt", resultAttempt, "current_attempt", entry.attempts)
		return nil, q.takePendingLocked()
	}

	// Future attempt: should never happen in normal flow.
	if resultAttempt > entry.attempts {
		return fmt.Errorf("task %s: result attempt %d exceeds current attempt %d", result.TaskID, resultAttempt, entry.attempts), q.takePendingLocked()
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
		q.transitionTaskLocked(entry, taskDone)
		entry.startedAt = nil
		entry.leaseAt = nil
		projectID, attempts := r.projectID, entry.attempts
		q.appendEffect(func(context.Context) {
			q.emit(projectID, "step.done", map[string]any{"run_id": runID, "step": stepName, "attempts": attempts})
		})
		step := &run.Step{
			ProjectID: r.projectID,
			RunID:     runID,
			StepName:  stepName,
			Status:    result.Status,
			StartedAt: &result.StartedAt,
			EndedAt:   &endedAt,
			Attempts:  entry.attempts,
			Error:     result.Error,
		}
		q.appendWrite("step done "+result.TaskID, func(ctx context.Context) error {
			return q.stepRepo.Upsert(ctx, step)
		})
		q.promoteReady(ctx, r)
		q.finalizeRunIfAllTerminalLocked(r)
	} else {
		q.failOrRetryLocked(ctx, r, entry, result.Error, &result.StartedAt, endedAt)
	}

	return nil, q.takePendingLocked()
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
		q.transitionTaskLocked(entry, taskRetrying)
		entry.assignedRuntimeID = ""
		entry.startedAt = nil
		entry.leaseAt = nil
		retryingStep := &run.Step{
			ProjectID: r.projectID,
			RunID:     runID,
			StepName:  stepName,
			Status:    string(taskRunning),
			StartedAt: startedAt,
			EndedAt:   &endedAt,
			Attempts:  entry.attempts,
			Error:     errMsg,
		}
		q.appendWrite("step retrying "+entry.task.ID, func(ctx context.Context) error {
			return q.stepRepo.Upsert(ctx, retryingStep)
		})
		q.scheduleRetryLocked(ctx, entry)
		return
	}

	q.stopEntryTimerLocked(entry)
	q.transitionTaskLocked(entry, taskFailed)
	entry.startedAt = nil
	entry.leaseAt = nil
	projectID, attempts := r.projectID, entry.attempts
	q.appendEffect(func(context.Context) {
		q.emit(projectID, "step.failed", map[string]any{"run_id": runID, "step": stepName, "attempts": attempts, "error": errMsg})
	})
	failedStep := &run.Step{
		ProjectID: r.projectID,
		RunID:     runID,
		StepName:  stepName,
		Status:    string(taskFailed),
		StartedAt: startedAt,
		EndedAt:   &endedAt,
		Attempts:  entry.attempts,
		Error:     errMsg,
	}
	q.appendWrite("step failed "+entry.task.ID, func(ctx context.Context) error {
		return q.stepRepo.Upsert(ctx, failedStep)
	})
	q.skipDownstream(ctx, r, stepName)
	q.finalizeRunIfAllTerminalLocked(r)
}

// finalizeRunIfAllTerminalLocked finalizes the run (as success/failed) once
// every step has reached a terminal state. Called with q.mu held.
func (q *Queue) finalizeRunIfAllTerminalLocked(r *runEntry) {
	if !q.allTerminal(r) {
		return
	}
	runStatus := run.StatusSuccess
	for _, e := range r.tasks {
		if e.status == taskFailed {
			runStatus = run.StatusFailed
			break
		}
	}
	q.finalizeRunLocked(r, runStatus, "run.completed")
}

// finalizeRunLocked is the single point through which a run reaches a
// terminal status — the consolidation of what used to be 4 independently
// implemented termination sites (normal completion, Cancel, TTL expiry, and
// RecoverWithEnv's all-terminal shortcut), each with its own DB write
// mechanism (some retried, one synchronous and un-retried), q.runs removal
// timing, event, and ReleaseRun/OnRunSuccess behavior. Uses
// FinalizeStatusCAS rather than a plain UPDATE so a second finalize attempt
// racing this one (e.g. a delayed cleanup sweep) can't clobber whichever
// terminal status won first — the DB row, not q.runs, is the actual source
// of truth for "is this run really done". Must be called with q.mu held.
func (q *Queue) finalizeRunLocked(r *runEntry, status, eventType string) {
	runID := r.runID
	projectID := r.projectID
	pl := r.pl
	finishedAt := time.Now()
	applied := false
	q.appendWrite("run finalized "+runID, func(ctx context.Context) error {
		var err error
		applied, err = q.runRepo.FinalizeStatusCAS(ctx, projectID, runID, status, &finishedAt)
		if err != nil {
			return err
		}
		if !applied {
			slog.Warn("queue: run was already terminal, not overwriting", "run_id", runID, "attempted_status", status)
		}
		return nil
	})
	delete(q.runs, runID)
	if owner, ok := q.backend.(pipelinedispatch.RunOwner); ok {
		owner.ReleaseRun(runID)
	}
	q.appendEffect(func(context.Context) {
		if applied {
			q.emit(projectID, eventType, map[string]any{"run_id": runID, "status": status})
		}
	})
	if q.OnRunOutcome != nil {
		onOutcome := q.OnRunOutcome
		q.appendEffect(func(context.Context) {
			if applied {
				onOutcome(context.Background(), projectID, runID, status, pl)
			}
		})
	}

	if status == run.StatusSuccess && q.OnRunSuccess != nil {
		onSuccess := q.OnRunSuccess
		q.appendEffect(func(context.Context) {
			if !applied {
				return
			}
			// Use a detached context so the callback isn't cancelled when the
			// HTTP request context that triggered this call ends.
			onSuccess(project.WithContext(context.Background(), project.Context{ID: projectID}), runID, pl)
		})
	}
}

// Cancel stops queue-owned work for a run and marks any non-terminal steps as canceled.
// Active backends may also receive a cancellation request to stop in-flight work.
// The local terminal state is committed unconditionally: Piper's cancel
// contract is "stop dispatch/retry and confirm locally," not "remote stop
// succeeded" — the remote CancelRun call below is best-effort observability,
// not a precondition, and (like every other termination path since the
// finalizeRunLocked consolidation) the run-status write itself is retried in
// the background rather than blocking this call on it.
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
				q.transitionTaskLocked(entry, taskCanceled)
				entry.startedAt = nil
				now := time.Now()
				canceledStep := &run.Step{
					ProjectID: r.projectID,
					RunID:     runID,
					StepName:  entry.step.Name,
					Status:    string(taskCanceled),
					StartedAt: startedAt,
					EndedAt:   &now,
					Attempts:  entry.attempts,
				}
				q.appendWrite("step canceled "+entry.task.ID, func(ctx context.Context) error {
					return q.stepRepo.Upsert(ctx, canceledStep)
				})
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
		q.finalizeRunLocked(r, run.StatusCanceled, "run.canceled")
	} else {
		// Not tracked in memory — already finished, or an id that was never
		// added. Still attempt the write (a genuinely-running-but-untracked
		// row should still end up canceled), but go through the same CAS
		// path as everything else: FinalizeStatusCAS's "not already
		// terminal" guard means a run that already reached success/failed
		// is left alone instead of being overwritten to "canceled".
		now := time.Now()
		q.appendWrite("run canceled (untracked) "+runID, func(ctx context.Context) error {
			_, err := q.runRepo.FinalizeStatusCAS(ctx, projectID, runID, run.StatusCanceled, &now)
			return err
		})
		q.appendEffect(func(context.Context) {
			q.emit(projectID, "run.canceled", map[string]any{"run_id": runID})
		})
	}
	outcome := q.takePendingLocked()
	q.mu.Unlock()
	flushPending(ctx, outcome)

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
			q.transitionTaskLocked(entry, taskReady)
			q.emit(r.projectID, "step.ready", map[string]any{"run_id": r.runID, "step": entry.step.Name, "task_id": entry.task.ID})
			q.dispatchIfNeeded(ctx, entry)
		}
	}
}

func (q *Queue) startTaskLocked(_ context.Context, runID string, entry *taskEntry) {
	entry.attempts++
	entry.task.Attempt = entry.attempts
	q.transitionTaskLocked(entry, taskRunning)
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
	// Emitted immediately (not deferred via appendEffect like the terminal
	// events below): a crash between this event and its write below just
	// means the step is recovered as never-started on restart — safe, no
	// duplicate-execution risk the way a lost step.done/step.failed would be.
	q.emit(entry.task.ProjectID, "step.running", map[string]any{"run_id": runID, "step": entry.step.Name, "task_id": entry.task.ID, "attempt": entry.attempts})
	runningStep := &run.Step{
		ProjectID: entry.task.ProjectID,
		RunID:     runID,
		StepName:  entry.step.Name,
		Status:    string(taskRunning),
		StartedAt: &now,
		Attempts:  entry.attempts,
	}
	q.appendWrite("step running "+entry.task.ID, func(ctx context.Context) error {
		return q.stepRepo.Upsert(ctx, runningStep)
	})
}

// scheduleTimeoutLocked arms entry.timer to fail (or retry) the step once
// its declared step.options.timeout deadline passes. Late real results are
// unaffected: Complete()'s terminal-idempotency check silently ignores a
// success that arrives after the step has already been marked failed here.
// Called with q.mu held.
func (q *Queue) scheduleTimeoutLocked(runID string, entry *taskEntry, deadline time.Time) {
	q.stopEntryTimerLocked(entry)
	q.wg.Add(1)
	entry.timer = time.AfterFunc(time.Until(deadline), func() {
		defer q.wg.Done()
		outcome := q.timeoutFiredLocked(runID, entry)
		// Use a fresh context, not the caller's: the ctx that originally
		// scheduled this timer (an HTTP request or dispatch-result context)
		// is long since canceled by the time this fires, which would make
		// the DB writes below fail immediately (matches the established
		// pattern in scheduleRetryLocked/requeue below).
		flushPending(context.Background(), outcome)
	})
}

func (q *Queue) timeoutFiredLocked(runID string, entry *taskEntry) pendingOutcome {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.serverCtx.Err() != nil || entry.status != taskRunning {
		return pendingOutcome{}
	}
	r, ok := q.runs[runID]
	if !ok {
		return pendingOutcome{}
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
	q.failOrRetryLocked(context.Background(), r, entry, "task execution timeout", entry.startedAt, time.Now())
	return q.takePendingLocked()
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
	q.wg.Add(1)
	entry.timer = time.AfterFunc(grace, func() {
		defer q.wg.Done()
		outcome := q.recoveryGraceFiredLocked(runID, entry)
		flushPending(context.Background(), outcome)
	})
}

func (q *Queue) recoveryGraceFiredLocked(runID string, entry *taskEntry) pendingOutcome {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.serverCtx.Err() != nil || entry.status != taskRecovering {
		return pendingOutcome{}
	}
	r, ok := q.runs[runID]
	if !ok {
		return pendingOutcome{}
	}
	entry.timer = nil
	// See scheduleTimeoutLocked: release the router-level reservation
	// ourselves (no Complete() result on this path) and use a fresh
	// context (the caller's is long dead by the time this fires).
	if owner, ok := q.backend.(pipelinedispatch.TaskOwner); ok {
		owner.ReleaseTask(entry.task.ID)
	}
	q.failOrRetryLocked(context.Background(), r, entry, "recovery grace period expired without worker reconnect", entry.startedAt, time.Now())
	return q.takePendingLocked()
}

// RenewLeases records that runtimeID is still executing the given tasks.
// Runtime liveness and task state remain separate: only explicit task IDs renew leases.
func (q *Queue) RenewLeases(runtimeID string, taskIDs []string) {
	if runtimeID == "" || len(taskIDs) == 0 {
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
		if entry.assignedRuntimeID == "" {
			if entry.task.RuntimeID != "" && entry.task.RuntimeID != runtimeID {
				continue
			}
			entry.assignedRuntimeID = runtimeID
		}
		if entry.assignedRuntimeID != runtimeID {
			continue
		}
		if entry.status == taskRecovering {
			// The owning worker reconnected and is still executing this task:
			// stop waiting out the grace period and resume normal running state.
			q.stopEntryTimerLocked(entry)
			q.transitionTaskLocked(entry, taskRunning)
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
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
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
			runtimeID := task.RuntimeID
			if owner, ok := b.(pipelinedispatch.TaskOwner); ok {
				if selectedRuntimeID := owner.OwnerForTask(task.ID); selectedRuntimeID != "" {
					runtimeID = selectedRuntimeID
				}
			}
			if completeErr := q.Complete(dispatchCtx, proto.TaskResult{
				TaskID:    task.ID,
				RuntimeID: runtimeID,
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
	q.transitionTaskLocked(entry, taskReady)
	entry.assignedRuntimeID = ""
	entry.startedAt = nil
	entry.leaseAt = nil
	slog.Info("task requeued after retryable dispatch failure", "task_id", entry.task.ID, "err", reason)
	q.stopEntryTimerLocked(entry)
	q.wg.Add(1)
	entry.timer = time.AfterFunc(2*time.Second, func() {
		defer q.wg.Done()
		outcome := q.requeuedDispatchFiredLocked(entry)
		flushPending(context.Background(), outcome)
	})
}

func (q *Queue) requeuedDispatchFiredLocked(entry *taskEntry) pendingOutcome {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry.timer = nil
	// serverCtx is cancelled on shutdown; without this check a timer that
	// outlives Close() (e.g. mid-flight when the process/test tears down)
	// would dispatch against an already-closed store.
	if q.serverCtx.Err() != nil || entry.status != taskReady {
		return pendingOutcome{}
	}
	q.dispatchIfNeeded(context.Background(), entry)
	return q.takePendingLocked()
}

func (q *Queue) scheduleRetryLocked(ctx context.Context, entry *taskEntry) {
	if q.retryDelay <= 0 {
		q.transitionTaskLocked(entry, taskReady)
		entry.timer = nil
		slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
		q.dispatchIfNeeded(ctx, entry)
		return
	}
	q.stopEntryTimerLocked(entry)
	q.wg.Add(1)
	entry.timer = time.AfterFunc(q.retryDelay, func() {
		defer q.wg.Done()
		outcome := q.retryFiredLocked(entry)
		flushPending(context.Background(), outcome)
	})
}

func (q *Queue) retryFiredLocked(entry *taskEntry) pendingOutcome {
	q.mu.Lock()
	defer q.mu.Unlock()
	if entry.status != taskRetrying {
		return pendingOutcome{}
	}
	entry.timer = nil
	q.transitionTaskLocked(entry, taskReady)
	slog.Info("task retry ready", "task_id", entry.task.ID, "attempt", entry.attempts+1, "max_attempts", entry.maxAttempts)
	q.dispatchIfNeeded(context.Background(), entry)
	return q.takePendingLocked()
}

// transitionTaskLocked is the single point through which every task/step
// status change happens — the mechanical replacement for what used to be 14
// scattered `entry.status = X` assignments across this file. It refuses to
// move a terminal entry (done/failed/skipped/canceled) to anything else:
// every terminal-state idempotency check already scattered across
// Complete/Cancel/Cleanup guards this before ever calling in, so this branch
// should never actually trigger — it exists as a hard backstop against a
// future call site that forgets to check first, rather than a real
// transition table (cancel/expiry are deliberately reachable from almost
// every non-terminal state, so a fully enumerated legal-edges list would
// mostly just restate that). Returns the prior status. Must be called with
// q.mu held.
func (q *Queue) transitionTaskLocked(entry *taskEntry, to taskStatus) taskStatus {
	from := entry.status
	switch from {
	case taskDone, taskFailed, taskSkipped, taskCanceled:
		if from != to {
			slog.Error("queue: refused to transition a terminal task entry", "task_id", entry.task.ID, "from", from, "to", to)
		}
		return from
	}
	entry.status = to
	return from
}

func (q *Queue) stopEntryTimerLocked(entry *taskEntry) {
	if entry.timer == nil {
		return
	}
	if entry.timer.Stop() {
		// Stop() reported it prevented the fire: the timer's own callback
		// (and the q.wg.Done it owns, see scheduleTimeoutLocked et al.) will
		// never run, so balance the wg.Add made when the timer was armed
		// here instead. If Stop() returns false the callback already fired
		// or is running and will call q.wg.Done() itself — never both.
		q.wg.Done()
	}
	entry.timer = nil
}

func (q *Queue) skipDownstream(ctx context.Context, r *runEntry, failedStep string) {
	for _, entry := range r.tasks {
		if entry.status != taskPending && entry.status != taskReady {
			continue
		}
		for _, dep := range entry.step.DependsOn {
			if dep == failedStep {
				q.transitionTaskLocked(entry, taskSkipped)
				skippedStep := &run.Step{
					ProjectID: r.projectID,
					RunID:     r.runID,
					StepName:  entry.step.Name,
					Status:    "skipped",
				}
				q.appendWrite("step skipped "+entry.task.ID, func(ctx context.Context) error {
					return q.stepRepo.Upsert(ctx, skippedStep)
				})
				projectID, runID, stepName, taskID := r.projectID, r.runID, entry.step.Name, entry.task.ID
				q.appendEffect(func(context.Context) {
					q.emit(projectID, "step.skipped", map[string]any{"run_id": runID, "step": stepName, "task_id": taskID, "failed_dep": failedStep})
				})
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
	outcome := q.cleanupLocked(ttl)
	flushPending(ctx, outcome)
}

func (q *Queue) cleanupLocked(ttl time.Duration) pendingOutcome {
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
					q.transitionTaskLocked(entry, taskFailed)
					entry.startedAt = nil
					entry.leaseAt = nil
					expiredStep := &run.Step{
						ProjectID: r.projectID,
						RunID:     runID,
						StepName:  entry.step.Name,
						Status:    string(taskFailed),
						EndedAt:   &now,
						Attempts:  entry.attempts,
						Error:     "task lease expired",
					}
					q.appendWrite("step expired "+entry.task.ID, func(ctx context.Context) error {
						return q.stepRepo.Upsert(ctx, expiredStep)
					})
				}
			}
			q.finalizeRunLocked(r, run.StatusFailed, "run.expired")
		}
	}
	return q.takePendingLocked()
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

// IsTracking reports whether runID is currently tracked in memory (added,
// recovered, and not yet finalized/canceled/expired). Used by a periodic
// DB-truth reconciler (see piper.go's recoverInterruptedRuns) to skip a run
// this Queue instance is still actively processing, so it doesn't get
// double-added — the reconciler only needs to act on rows the DB says are
// non-terminal but that no longer show up here.
func (q *Queue) IsTracking(runID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.runs[runID]
	return ok
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
