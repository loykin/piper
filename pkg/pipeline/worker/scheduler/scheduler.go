// Package scheduler is the worker-owned DAG state machine: dependency
// promotion, retry, timeout, and finalize decisions for a run's steps,
// executed locally via pkg/pipeline/worker/driver.Driver instead of the
// master's internal/queue.Queue deciding and dispatching one step at a time.
//
// A single RunScheduler owns exactly one run's DAG. It is deliberately built
// only in terms of driver.Driver's four infra-agnostic methods
// (Start/Wait/Stop/Recover) — see driver.go's own doc comment — so the same
// implementation works unchanged for baremetal, docker, and k8s: the k8s
// driver's poll-based Job reconciliation is already hidden behind Wait(),
// same as every other driver.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

type stepStatus string

const (
	stepPending  stepStatus = "pending"
	stepReady    stepStatus = "ready"
	stepRunning  stepStatus = "running"
	stepRetrying stepStatus = "retrying"
	stepDone     stepStatus = stepStatus(run.StepStatusDone)
	stepFailed   stepStatus = stepStatus(run.StepStatusFailed)
	stepSkipped  stepStatus = stepStatus(run.StepStatusSkipped)
	stepCanceled stepStatus = stepStatus(run.StepStatusCanceled)
)

func isTerminal(s stepStatus) bool {
	switch s {
	case stepDone, stepFailed, stepSkipped, stepCanceled:
		return true
	}
	return false
}

// stepEntry mirrors internal/queue/queue.go's taskEntry, scoped to one
// worker-local step execution instead of a remote-dispatch task.
type stepEntry struct {
	step      pipeline.Step
	status    stepStatus
	attempts  int
	startedAt *time.Time
	handle    pdriver.Handle
	hasHandle bool
	cancelFn  context.CancelFunc
}

// BuildExecSpec builds the driver.ExecSpec for a step's task. Kept
// caller-supplied because image/namespace resolution is driver-specific —
// see pkg/pipeline/worker/worker.go's dispatch, which resolves Docker images
// in the worker layer before ever calling driver.Start; a K8s worker
// resolves image+namespace the same way. RunScheduler itself never needs to
// know about any of that.
type BuildExecSpec func(task *proto.Task) (pdriver.ExecSpec, error)

// StepReporter durably reports state transitions to the master's DB via the
// pipeline.step_upsert / pipeline.run_finalize worker-initiated RPCs (see
// pipeline_db_handlers.go). Implementations should be backed by
// driver.RequestOutbox so a transient tunnel disconnect doesn't lose a
// report — see NewOutboxReporter.
type StepReporter interface {
	UpsertStep(s *run.Step) error
	FinalizeRun(status string, endedAt time.Time) error
}

// AfterStart is called once per step immediately after driver.Start
// succeeds (and this scheduler has confirmed the step wasn't canceled
// mid-start), with the exact task/spec/handle Start was called with and the
// step's own execution context — the same one driver.Wait is called with,
// canceled when the step finishes or is canceled. It exists for
// driver-specific side effects that don't fit driver.Driver's
// infra-agnostic Start/Wait/Stop/Recover contract: the k8s worker's live
// pod log streaming needs the k8s API client and the exact ExecSpec chosen
// for this step, neither of which Handle/Exit carry. Called synchronously
// but must not block — an implementation that needs to run for the step's
// whole lifetime (like log streaming) must spawn its own goroutine, exactly
// as pkg/pipeline/worker/worker.go's per-step dispatch already does for its
// own log sink today. Most drivers (baremetal, docker) need no AfterStart
// at all — leave it nil.
type AfterStart func(stepCtx context.Context, task *proto.Task, spec pdriver.ExecSpec, handle pdriver.Handle)

// Options configures a new RunScheduler.
type Options struct {
	Driver        pdriver.Driver
	Report        StepReporter
	BuildExecSpec BuildExecSpec
	AfterStart    AfterStart
	// MaxAttempts is the total number of tries per step, including the
	// first. <1 is treated as 1 (no retries).
	MaxAttempts int
	// RetryDelay is the wait before a retried step re-starts. 0 means
	// immediately.
	RetryDelay time.Duration
	WorkerID   string
	// OnFinalize is called exactly once, after the run reaches success,
	// failed, or canceled and FinalizeRun has been reported — used by
	// Registry to drop the RunScheduler from its map.
	OnFinalize func(status string)
}

// RunScheduler owns the DAG state machine for a single run: dependency
// promotion, retry, timeout, and the decision of when the run itself is
// done. Safe for concurrent use. Unlike internal/queue.Queue, there is no
// shared lock across runs — each RunScheduler has its own mutex, since a
// worker's runs never share state (see AGENTS.md's "one run = one worker").
type RunScheduler struct {
	mu          sync.Mutex
	projectID   string
	runID       string
	workerID    string
	pl          *pipeline.Pipeline
	steps       map[string]*stepEntry
	driver      pdriver.Driver
	report      StepReporter
	buildSpec   BuildExecSpec
	afterStart  AfterStart
	maxAttempts int
	retryDelay  time.Duration
	dispatch    proto.RunDispatch
	finalized   bool
	onFinalize  func(status string)
	// wg tracks every goroutine this scheduler spawns on its own initiative
	// (one per step execution) so Close can wait for them to actually stop
	// touching driver/report state before the caller tears anything down.
	wg sync.WaitGroup
}

// New parses dispatch.PipelineYAML and builds a RunScheduler ready to Start.
func New(dispatch proto.RunDispatch, opts Options) (*RunScheduler, error) {
	if opts.Driver == nil {
		return nil, fmt.Errorf("scheduler: driver is required")
	}
	if opts.Report == nil {
		return nil, fmt.Errorf("scheduler: reporter is required")
	}
	if opts.BuildExecSpec == nil {
		return nil, fmt.Errorf("scheduler: BuildExecSpec is required")
	}
	pl, err := pipeline.Parse([]byte(dispatch.PipelineYAML))
	if err != nil {
		return nil, fmt.Errorf("scheduler: parse pipeline yaml: %w", err)
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	steps := make(map[string]*stepEntry, len(pl.Spec.Steps))
	for i := range pl.Spec.Steps {
		steps[pl.Spec.Steps[i].Name] = &stepEntry{step: pl.Spec.Steps[i], status: stepPending}
	}
	return &RunScheduler{
		projectID:   dispatch.ProjectID,
		runID:       dispatch.RunID,
		workerID:    opts.WorkerID,
		pl:          pl,
		steps:       steps,
		driver:      opts.Driver,
		report:      opts.Report,
		buildSpec:   opts.BuildExecSpec,
		afterStart:  opts.AfterStart,
		maxAttempts: maxAttempts,
		retryDelay:  opts.RetryDelay,
		dispatch:    dispatch,
		onFinalize:  opts.OnFinalize,
	}, nil
}

// Start begins DAG promotion: every step with no unmet dependencies is
// started immediately. Call exactly once per RunScheduler.
func (rs *RunScheduler) Start() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.promoteReadyLocked()
}

// Cancel stops every active step (via driver.Stop, using the handle
// captured under the lock — matches pkg/pipeline/worker/worker.go's own
// cancelRun, which re-queries under the same discipline to avoid racing the
// observe goroutine's cleanup), marks every remaining non-terminal step
// canceled, and finalizes the run as canceled. Safe to call on an
// already-finalized scheduler (no-ops). Mirrors
// internal/queue/queue.go's Cancel, scoped to one run with no remote
// backend call needed — this scheduler already *is* the thing that would
// have received that call.
func (rs *RunScheduler) Cancel() error {
	rs.mu.Lock()
	if rs.finalized {
		rs.mu.Unlock()
		return nil
	}
	var toStop []pdriver.Handle
	now := time.Now()
	for _, e := range rs.steps {
		if isTerminal(e.status) {
			continue
		}
		startedAt := e.startedAt
		wasRunning := e.status == stepRunning
		hasHandle := e.hasHandle
		handle := e.handle
		if e.cancelFn != nil {
			e.cancelFn()
		}
		e.status = stepCanceled
		e.startedAt = nil
		rs.reportStepLocked(e, run.StepStatusCanceled, "", startedAt, &now)
		if wasRunning && hasHandle {
			toStop = append(toStop, handle)
		}
	}
	rs.finalizeLocked(run.StatusCanceled)
	rs.mu.Unlock()

	var errs []error
	for _, h := range toStop {
		if err := rs.driver.Stop(context.Background(), h, 10*time.Second); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", h.RuntimeKey, err))
		}
	}
	return errors.Join(errs...)
}

// Close waits for every in-flight step-execution goroutine to finish,
// bounded by ctx. Call before discarding a RunScheduler whose steps may
// still be running (e.g. worker shutdown) — mirrors internal/queue.Queue's
// own Close.
func (rs *RunScheduler) Close(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		rs.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func depsAllDone(deps []string, done map[string]bool) bool {
	for _, d := range deps {
		if !done[d] {
			return false
		}
	}
	return true
}

func (rs *RunScheduler) doneNamesLocked() map[string]bool {
	done := make(map[string]bool, len(rs.steps))
	for name, e := range rs.steps {
		if e.status == stepDone || e.status == stepSkipped {
			done[name] = true
		}
	}
	return done
}

func (rs *RunScheduler) allTerminalLocked() bool {
	for _, e := range rs.steps {
		if !isTerminal(e.status) {
			return false
		}
	}
	return true
}

// promoteReadyLocked starts every step whose dependencies are now all done.
// Ported from internal/queue/queue.go's promoteReady — same full-rescan
// approach (dependency-readiness is O(steps) recomputed on every change
// rather than incrementally tracked), which is cheap enough at pipeline
// scale and much simpler to reason about. Must be called with rs.mu held.
func (rs *RunScheduler) promoteReadyLocked() {
	done := rs.doneNamesLocked()
	for _, e := range rs.steps {
		if e.status != stepPending {
			continue
		}
		if depsAllDone(e.step.DependsOn, done) {
			e.status = stepReady
			rs.startStepLocked(e)
		}
	}
}

// startStepLocked transitions e to running, reports it, and spawns the
// goroutine that actually calls driver.Start/Wait. Must be called with
// rs.mu held.
func (rs *RunScheduler) startStepLocked(e *stepEntry) {
	e.attempts++
	e.status = stepRunning
	now := time.Now()
	e.startedAt = &now

	task, err := rs.buildTask(e, now)
	if err != nil {
		slog.Error("scheduler: build task failed", "run_id", rs.runID, "step", e.step.Name, "err", err)
		rs.failOrRetryLocked(e, "build task: "+err.Error(), &now, time.Now())
		return
	}

	rs.reportStepLocked(e, run.StepStatusRunning, "", &now, nil)

	stepCtx, cancel := context.WithCancel(context.Background())
	if task.Deadline != nil {
		stepCtx, cancel = context.WithDeadline(context.Background(), *task.Deadline)
	}
	e.cancelFn = cancel

	rs.wg.Add(1)
	go rs.runStep(stepCtx, e, task)
}

// buildTask constructs the per-step proto.Task from the run's dispatch
// payload — the worker-local equivalent of
// internal/queue/queue.go's addWithEnvLocked task construction.
func (rs *RunScheduler) buildTask(e *stepEntry, now time.Time) (*proto.Task, error) {
	stepJSON, err := json.Marshal(&e.step)
	if err != nil {
		return nil, err
	}
	pipelineJSON, err := json.Marshal(rs.pl)
	if err != nil {
		return nil, err
	}
	task := &proto.Task{
		ProjectID:    rs.projectID,
		ID:           MakeTaskID(rs.runID, e.step.Name),
		RunID:        rs.runID,
		StepName:     e.step.Name,
		Step:         stepJSON,
		Pipeline:     pipelineJSON,
		WorkDir:      rs.dispatch.WorkDir,
		OutputDir:    rs.dispatch.OutputDir,
		CreatedAt:    now,
		Label:        e.step.Driver.Placement.Label,
		WorkerID:     rs.workerID,
		Attempt:      e.attempts,
		Vars:         rs.dispatch.Vars,
		RunParams:    rs.dispatch.RunParams,
		Env:          append([]string{}, rs.dispatch.Env[e.step.Name]...),
		StorageURL:   rs.dispatch.StorageURL,
		StorageToken: rs.dispatch.StorageToken,
	}
	if timeout := e.step.Options.Timeout; timeout > 0 {
		deadline := now.Add(time.Duration(timeout) * time.Second)
		task.Deadline = &deadline
	}
	return task, nil
}

// runStep runs outside rs.mu: it calls the (potentially slow) driver.Start,
// then blocks on driver.Wait until the step reaches a terminal state or
// stepCtx is cancelled (by Cancel, or by the deadline set on task.Deadline).
func (rs *RunScheduler) runStep(stepCtx context.Context, e *stepEntry, task *proto.Task) {
	defer rs.wg.Done()

	spec, err := rs.buildSpec(task)
	if err != nil {
		rs.onStepFailed(e, "build exec spec: "+err.Error())
		return
	}
	if spec.LogSink != nil {
		// Owned by this goroutine for the step's whole lifetime — stopped
		// once Start+Wait (or a Start failure) concludes, mirroring
		// pkg/pipeline/worker/worker.go's Worker.observe, which stops the
		// same kind of sink right after driver.Wait returns.
		defer spec.LogSink.Stop()
	}
	handle, err := rs.driver.Start(stepCtx, task, spec)
	if err != nil {
		rs.onStepFailed(e, "start step: "+err.Error())
		return
	}

	rs.mu.Lock()
	if e.status != stepRunning {
		// Canceled while starting (Cancel already moved this entry to
		// stepCanceled and reported it) — stop what was just started instead
		// of publishing it as active.
		rs.mu.Unlock()
		_ = rs.driver.Stop(context.Background(), handle, 10*time.Second)
		return
	}
	e.handle = handle
	e.hasHandle = true
	rs.mu.Unlock()

	if rs.afterStart != nil {
		rs.afterStart(stepCtx, task, spec, handle)
	}

	exit, err := rs.driver.Wait(stepCtx, handle)
	if err != nil {
		if errors.Is(stepCtx.Err(), context.DeadlineExceeded) {
			_ = rs.driver.Stop(context.Background(), handle, 10*time.Second)
			rs.onStepFailed(e, "task execution timeout")
			return
		}
		// Otherwise this is a cancellation (Cancel already owns the
		// terminal-state transition and driver.Stop call for that path) or a
		// shutdown — nothing further to do here.
		return
	}
	rs.onStepExit(e, handle, exit)
}

func (rs *RunScheduler) onStepFailed(e *stepEntry, errMsg string) {
	now := time.Now()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if isTerminal(e.status) {
		return
	}
	rs.failOrRetryLocked(e, errMsg, e.startedAt, now)
}

func (rs *RunScheduler) onStepExit(e *stepEntry, handle pdriver.Handle, exit pdriver.Exit) {
	result := buildResult(rs.workerID, handle, exit)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if isTerminal(e.status) {
		return
	}
	if result.Status == proto.TaskStatusDone {
		rs.completeStepLocked(e, result.EndedAt)
		return
	}
	startedAt := result.StartedAt
	rs.failOrRetryLocked(e, result.Error, &startedAt, result.EndedAt)
}

// buildResult mirrors pkg/pipeline/worker/worker.go's (unexported)
// Worker.buildResult — same three-way precedence (pre-parsed Result,
// InfraFailure, or a result file written by piper agent exec).
func buildResult(workerID string, handle pdriver.Handle, exit pdriver.Exit) proto.TaskResult {
	if exit.Result != nil {
		r := *exit.Result
		r.WorkerID = workerID
		return r
	}
	if exit.InfraFailure != nil {
		return proto.TaskResult{
			TaskID:    handle.TaskID,
			WorkerID:  workerID,
			Status:    proto.TaskStatusFailed,
			Error:     exit.InfraFailure.Error(),
			StartedAt: time.Now(),
			EndedAt:   time.Now(),
			Attempt:   handle.Attempt,
		}
	}
	if exit.ResultPath != "" {
		if data, err := os.ReadFile(exit.ResultPath); err == nil {
			if r, err := agent.ReadAgentResult(data); err == nil {
				r.WorkerID = workerID
				return r
			}
		}
	}
	return proto.TaskResult{
		TaskID:   handle.TaskID,
		WorkerID: workerID,
		Status:   proto.TaskStatusFailed,
		Error:    "result unavailable after job completion",
		EndedAt:  time.Now(),
		Attempt:  handle.Attempt,
	}
}

// completeStepLocked mirrors internal/queue/queue.go's completeLocked's
// taskDone branch. Must be called with rs.mu held.
func (rs *RunScheduler) completeStepLocked(e *stepEntry, endedAt time.Time) {
	startedAt := e.startedAt
	e.status = stepDone
	e.startedAt = nil
	rs.reportStepLocked(e, run.StepStatusDone, "", startedAt, &endedAt)
	rs.promoteReadyLocked()
	rs.finalizeIfAllTerminalLocked()
}

// failOrRetryLocked marks e retrying (if attempts remain) or terminally
// failed (skipping downstream steps and finalizing the run if that was the
// last non-terminal step). Ported from internal/queue/queue.go's
// failOrRetryLocked. Must be called with rs.mu held.
func (rs *RunScheduler) failOrRetryLocked(e *stepEntry, errMsg string, startedAt *time.Time, endedAt time.Time) {
	if e.attempts < rs.maxAttempts {
		e.status = stepRetrying
		e.startedAt = nil
		rs.reportStepLocked(e, run.StepStatusRunning, errMsg, startedAt, &endedAt)
		rs.scheduleRetryLocked(e)
		return
	}

	e.status = stepFailed
	e.startedAt = nil
	rs.reportStepLocked(e, run.StepStatusFailed, errMsg, startedAt, &endedAt)
	rs.skipDownstreamLocked(e.step.Name)
	rs.finalizeIfAllTerminalLocked()
}

// scheduleRetryLocked mirrors internal/queue/queue.go's scheduleRetryLocked.
// Must be called with rs.mu held.
func (rs *RunScheduler) scheduleRetryLocked(e *stepEntry) {
	if rs.retryDelay <= 0 {
		e.status = stepReady
		rs.startStepLocked(e)
		return
	}
	rs.wg.Add(1)
	time.AfterFunc(rs.retryDelay, func() {
		defer rs.wg.Done()
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if e.status != stepRetrying {
			return
		}
		e.status = stepReady
		rs.startStepLocked(e)
	})
}

// skipDownstreamLocked mirrors internal/queue/queue.go's skipDownstream.
// Must be called with rs.mu held.
func (rs *RunScheduler) skipDownstreamLocked(failedStep string) {
	for _, e := range rs.steps {
		if e.status != stepPending && e.status != stepReady {
			continue
		}
		for _, dep := range e.step.DependsOn {
			if dep == failedStep {
				e.status = stepSkipped
				rs.reportStepLocked(e, run.StepStatusSkipped, "", nil, nil)
				rs.skipDownstreamLocked(e.step.Name)
				break
			}
		}
	}
}

// finalizeIfAllTerminalLocked mirrors internal/queue/queue.go's
// finalizeRunIfAllTerminalLocked. Must be called with rs.mu held.
func (rs *RunScheduler) finalizeIfAllTerminalLocked() {
	if rs.finalized || !rs.allTerminalLocked() {
		return
	}
	status := run.StatusSuccess
	for _, e := range rs.steps {
		if e.status == stepFailed {
			status = run.StatusFailed
			break
		}
	}
	rs.finalizeLocked(status)
}

// finalizeLocked reports the run's terminal status exactly once. Unlike
// internal/queue/queue.go's finalizeRunLocked, there is no separate
// in-memory run map to remove from — OnFinalize (typically Registry
// dropping this scheduler from its map) plays that role. Must be called
// with rs.mu held.
func (rs *RunScheduler) finalizeLocked(status string) {
	rs.finalized = true
	if err := rs.report.FinalizeRun(status, time.Now()); err != nil {
		slog.Error("scheduler: report run finalize failed", "run_id", rs.runID, "status", status, "err", err)
	}
	if rs.onFinalize != nil {
		rs.onFinalize(status)
	}
}

func (rs *RunScheduler) reportStepLocked(e *stepEntry, status, errMsg string, startedAt, endedAt *time.Time) {
	s := &run.Step{
		ProjectID: rs.projectID,
		RunID:     rs.runID,
		StepName:  e.step.Name,
		Status:    status,
		StartedAt: startedAt,
		EndedAt:   endedAt,
		Attempts:  e.attempts,
		Error:     errMsg,
		WorkerID:  rs.workerID,
	}
	if err := rs.report.UpsertStep(s); err != nil {
		slog.Error("scheduler: report step upsert failed", "run_id", rs.runID, "step", e.step.Name, "status", status, "err", err)
	}
}

// MakeTaskID builds a task ID from runID and stepName, matching
// internal/queue.MakeTaskID's "{runID}:{stepName}" shape — kept as a local,
// independent copy rather than importing internal/queue, since that package
// is superseded by this one (see the Phase 2/3 design's sub-milestone (e)).
func MakeTaskID(runID, stepName string) string {
	return runID + ":" + stepName
}
