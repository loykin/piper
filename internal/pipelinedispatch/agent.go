package pipelinedispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
)

type AgentRPC interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

type AgentBackend struct {
	router      *iagent.Router
	rpc         AgentRPC
	runRepo     run.Repository // may be nil: confirmRunBinding then no-ops
	podPolicies iagent.WorkerPodPolicyRepository
	taskAgents  sync.Map // task id -> pipelineTaskAgent

	runMu        sync.Mutex
	runAgents    map[string]*pipelineRunAgent // run id -> fixed agent for the whole run
	canceledRuns map[string]struct{}          // run id -> canceled before any binding existed
}

type pipelineRunAgent struct {
	AgentID   string
	Namespace string
	Committed bool
	Pending   int
	// bindingDone is closed once the confirmRunBinding call for this run
	// agent has finished (success or failure); bindingErr holds the result
	// (nil on success), written before the close. A concurrent Dispatch call
	// for another root step of the same run — which can start before the
	// first one's binding confirmation finishes — finds this runAgent
	// already in b.runAgents and must wait on bindingDone instead of
	// proceeding straight to SendRPC; otherwise it could dispatch a workload
	// before runs.worker_id is durable, defeating the ordering
	// confirmRunBinding exists for. Reads of bindingErr are only valid after
	// observing bindingDone closed (or by the goroutine that wrote it) —
	// the channel close is what establishes the happens-before.
	bindingDone chan struct{}
	bindingErr  error
}

type pipelineTaskAgent struct {
	AgentID string
}

// NewAgentBackend constructs an AgentBackend. runRepo is used to persist
// runs.worker_id as the authoritative record of run-to-worker binding before
// any dispatch RPC is sent (see confirmRunBinding) — pass nil to skip this
// (e.g. an embedding/test setup with no DB-backed run.Repository), which
// only means the binding record isn't persisted, not that dispatch fails.
func NewAgentBackend(router *iagent.Router, rpc AgentRPC, runRepo run.Repository, policies ...iagent.WorkerPodPolicyRepository) *AgentBackend {
	b := &AgentBackend{
		router:       router,
		rpc:          rpc,
		runRepo:      runRepo,
		runAgents:    make(map[string]*pipelineRunAgent),
		canceledRuns: make(map[string]struct{}),
	}
	if len(policies) > 0 {
		b.podPolicies = policies[0]
	}
	return b
}

func (b *AgentBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	if b == nil || b.router == nil || b.rpc == nil {
		return fmt.Errorf("pipeline agent backend is not configured")
	}
	placement, err := taskPlacement(task)
	if err != nil {
		return err
	}
	b.runMu.Lock()
	runAgent, bound := b.runAgents[task.RunID]
	if !bound {
		agentInfo, selectErr := b.router.Reserve(iagent.WorkloadPipeline, placement)
		if selectErr != nil {
			b.runMu.Unlock()
			var ambiguous *iagent.AmbiguousInfrastructureError
			if errors.As(selectErr, &ambiguous) {
				// A configuration problem (no driver.placement.runtime to
				// disambiguate multiple infrastructure types), not a
				// transient capacity issue — retrying changes nothing until
				// the pipeline is fixed.
				return &DispatchError{Retryable: false, Err: selectErr}
			}
			// No available worker — retryable so the queue re-attempts after a short delay.
			return &DispatchError{Retryable: true, Err: selectErr}
		}
		runAgent = &pipelineRunAgent{
			AgentID:     agentInfo.ID,
			Namespace:   placement.Namespace,
			bindingDone: make(chan struct{}),
		}
		b.runAgents[task.RunID] = runAgent
	} else if reserveErr := b.router.ReserveAgent(runAgent.AgentID, iagent.WorkloadPipeline); reserveErr != nil {
		b.runMu.Unlock()
		return &DispatchError{Retryable: true, Err: reserveErr}
	}
	newBinding := !bound
	runAgent.Pending++
	b.runMu.Unlock()

	// Persist runs.worker_id before this run's workload can possibly start —
	// see confirmRunBinding. The first task of a run does the actual binding
	// call; any concurrent Dispatch for another root step of the same run
	// (which can reach this point before the first one's binding call
	// returns) waits on bindingDone instead of skipping straight past —
	// otherwise it could send its own dispatch RPC while the binding is
	// still unconfirmed.
	if newBinding {
		runAgent.bindingErr = b.confirmRunBinding(ctx, task, runAgent.AgentID)
		close(runAgent.bindingDone)
	} else {
		select {
		case <-runAgent.bindingDone:
		case <-ctx.Done():
			// This goroutine already holds a router reservation from the
			// ReserveAgent call above (the `bound` branch) — bailing out
			// here without releasing it would leak that capacity slot
			// forever, since nothing else references it.
			b.router.Release(runAgent.AgentID)
			b.runMu.Lock()
			b.unwindRunAgentLocked(task.RunID, runAgent)
			b.runMu.Unlock()
			return ctx.Err()
		}
	}
	if runAgent.bindingErr != nil {
		b.router.Release(runAgent.AgentID)
		b.runMu.Lock()
		b.unwindRunAgentLocked(task.RunID, runAgent)
		b.runMu.Unlock()
		return runAgent.bindingErr
	}

	taskCopy := *task
	taskCopy.WorkerID = runAgent.AgentID
	if b.podPolicies != nil {
		policy, pErr := b.podPolicies.Get(ctx, runAgent.AgentID)
		if pErr != nil {
			slog.Warn("pipeline: pod policy lookup failed, proceeding without policy",
				"worker_id", runAgent.AgentID, "err", pErr)
		} else if policy != nil {
			if merged, mErr := applyPodPolicyToPipeline(taskCopy.Pipeline, policy.PodTemplate); mErr == nil {
				taskCopy.Pipeline = merged
			} else {
				slog.Warn("pipeline: pod policy merge failed, using original pipeline",
					"worker_id", runAgent.AgentID, "err", mErr)
			}
		}
	}
	b.runMu.Lock()
	_, tombstoned := b.canceledRuns[task.RunID]
	b.runMu.Unlock()
	if tombstoned {
		b.router.Release(runAgent.AgentID)
		b.runMu.Lock()
		b.unwindRunAgentLocked(task.RunID, runAgent)
		delete(b.canceledRuns, task.RunID)
		b.runMu.Unlock()
		return fmt.Errorf("pipeline agent dispatch: run %s was canceled before dispatch", task.RunID)
	}

	b.taskAgents.Store(task.ID, pipelineTaskAgent{AgentID: runAgent.AgentID})
	if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineDispatch, &taskCopy, nil); err != nil {
		b.taskAgents.Delete(task.ID)
		b.router.Release(runAgent.AgentID)
		b.runMu.Lock()
		b.unwindRunAgentLocked(task.RunID, runAgent)
		b.runMu.Unlock()
		// Worker capacity refusal: BusyErrorMarker is embedded in the error string
		// because gRPC serialises the error to a plain string on the wire.
		if strings.Contains(err.Error(), iagent.BusyErrorMarker) {
			return &DispatchError{Retryable: true, Err: err}
		}
		return fmt.Errorf("pipeline agent dispatch: %w", err)
	}
	b.runMu.Lock()
	runAgent.Pending--
	runAgent.Committed = true
	// Re-check canceledRuns after the dispatch RPC has actually gone out,
	// not just before: SendRPC (a network call) is a window CancelRun can
	// run inside of. If Cancel lands in that exact window, it observes
	// Committed still false (the write above hasn't happened yet) and takes
	// the tombstone-only branch instead of sending a real cancel RPC —
	// because there was nothing to cancel *yet*, as far as it could tell.
	// Without this re-check, that tombstone would sit unconsumed and the
	// workload — which the dispatch RPC just above actually started on the
	// worker — would keep running uncanceled despite CancelRun having
	// returned success to its caller.
	_, tombstonedAfterSend := b.canceledRuns[task.RunID]
	if tombstonedAfterSend {
		delete(b.canceledRuns, task.RunID)
	}
	b.runMu.Unlock()
	if tombstonedAfterSend {
		// Send the real cancel now. This is guaranteed to reach the worker
		// strictly after the dispatch RPC just above: both go out over the
		// same tunnel connection, whose single writer serializes frames in
		// call order (see grpcagent's workerConn.sendRPC/writeMu) — so the
		// worker cannot receive this cancel before it receives the dispatch
		// it's canceling, which would otherwise let the dispatch "win" and
		// start the run anyway regardless of send order here.
		if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineCancelRun, map[string]any{
			"run_id":    task.RunID,
			"namespace": runAgent.Namespace,
		}, nil); err != nil {
			slog.Warn("pipeline: best-effort cancel after dispatch raced a cancel failed", "run_id", task.RunID, "err", err)
		}
		return fmt.Errorf("pipeline agent dispatch: run %s was canceled while its dispatch RPC was in flight", task.RunID)
	}
	return nil
}

// confirmRunBinding persists runs.worker_id for task.RunID as workerID
// before Dispatch ever sends the workload to that worker. This ordering
// matters: once dispatched, the worker can report back (step_upsert,
// run_finalize) essentially immediately — for a fast single-step run, often
// before this function would have returned if it ran concurrently with (or
// after) the dispatch RPC instead of strictly before it. Those handlers
// treat runs.worker_id as their authorization root (see
// pipeline_db_handlers.go), so it must already be durable by the time the
// worker could possibly call them, not merely "eventually consistent."
func (b *AgentBackend) confirmRunBinding(ctx context.Context, task *proto.Task, workerID string) error {
	if b.runRepo == nil {
		return nil
	}
	applied, err := b.runRepo.SetWorkerID(ctx, task.ProjectID, task.RunID, workerID)
	if err != nil {
		return &DispatchError{Retryable: true, Err: fmt.Errorf("confirm run binding: %w", err)}
	}
	if applied {
		return nil
	}
	// Not applied: the row already had a worker_id — most likely this is a
	// retried Dispatch call (e.g. after a transient failure earlier in this
	// same function) re-confirming the same binding, or state surviving a
	// master restart. Read it back to tell that apart from a genuine
	// conflict (the run bound to a *different* worker), which must not be
	// allowed to proceed silently.
	existing, getErr := b.runRepo.Get(ctx, task.ProjectID, task.RunID)
	if getErr != nil {
		return &DispatchError{Retryable: true, Err: fmt.Errorf("confirm run binding: read back: %w", getErr)}
	}
	if existing == nil || existing.WorkerID != workerID {
		return &DispatchError{Retryable: false, Err: fmt.Errorf("pipeline agent dispatch: run %s is already bound to a different worker", task.RunID)}
	}
	return nil
}

// unwindRunAgentLocked reverts the bookkeeping a Dispatch call speculatively
// added for runID when that call ultimately fails: drops the pending count,
// and removes the run-agent binding entirely if nothing ever committed to
// it (so the next Dispatch call re-selects a worker from scratch instead of
// retrying against a binding that never actually took). Must be called with
// runMu held.
func (b *AgentBackend) unwindRunAgentLocked(runID string, runAgent *pipelineRunAgent) {
	runAgent.Pending--
	if !runAgent.Committed && runAgent.Pending == 0 && b.runAgents[runID] == runAgent {
		delete(b.runAgents, runID)
	}
}

func (b *AgentBackend) OwnerForTask(taskID string) string {
	owner, ok := b.taskAgents.Load(taskID)
	if !ok {
		return ""
	}
	return owner.(pipelineTaskAgent).AgentID
}

func (b *AgentBackend) ReleaseTask(taskID string) {
	owner, ok := b.taskAgents.LoadAndDelete(taskID)
	if !ok {
		return
	}
	b.router.Release(owner.(pipelineTaskAgent).AgentID)
}

func (b *AgentBackend) ReleaseRun(runID string) {
	b.runMu.Lock()
	delete(b.runAgents, runID)
	delete(b.canceledRuns, runID)
	b.runMu.Unlock()
}

func (b *AgentBackend) CancelRun(ctx context.Context, runID string) error {
	if b == nil || b.rpc == nil {
		return fmt.Errorf("pipeline agent backend is not configured")
	}
	b.runMu.Lock()
	runAgent, ok := b.runAgents[runID]
	if !ok || !runAgent.Committed {
		// Either no binding exists yet, or one exists but nothing has
		// actually reached the worker yet: Dispatch may still be waiting on
		// confirmRunBinding's DB round trip (its own, or — for a concurrent
		// root step — another Dispatch call's, via the bindingDone barrier),
		// or on SendRPC itself. Tombstone unconditionally rather than
		// sending a cancel RPC for a workload that was never dispatched:
		// every Dispatch call for this run re-checks canceledRuns right
		// before it would send the dispatch RPC (below, after the binding
		// barrier), and aborts if it's set — including ones already in
		// flight right now. This is what stops a workload from starting
		// after the run was already canceled, instead of depending on
		// exactly how the cancel and the in-flight binding/dispatch happen
		// to interleave.
		b.canceledRuns[runID] = struct{}{}
		b.runMu.Unlock()
		return nil
	}
	b.runMu.Unlock()
	if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineCancelRun, map[string]any{
		"run_id":    runID,
		"namespace": runAgent.Namespace,
	}, nil); err != nil {
		return fmt.Errorf("pipeline agent cancel: %w", err)
	}
	b.ReleaseRun(runID)
	return nil
}

func taskPlacement(task *proto.Task) (iagent.Placement, error) {
	if task == nil {
		return iagent.Placement{}, fmt.Errorf("task is required")
	}
	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return iagent.Placement{}, fmt.Errorf("unmarshal pipeline: %w", err)
	}
	var defaults pipeline.PipelineDefaults
	if pl.Spec.Defaults != nil {
		defaults = *pl.Spec.Defaults
	}
	var ns string
	if defaults.Driver.K8s != nil {
		ns = defaults.Driver.K8s.Namespace
	}
	// task.WorkerID takes priority over the manifest's own
	// defaults.driver.placement.worker when set. For a normal (non-recovery)
	// dispatch these are already identical — queue.go's task-construction
	// path (addWithEnvLocked/recoverWithEnvLocked) derives task.WorkerID
	// from this exact manifest field, so this branch is a no-op there. It
	// only matters on recovery: a run that was auto-assigned (no explicit
	// placement.worker in the manifest at all) still has its actual binding
	// in runs.worker_id, and recoverWithEnvLocked puts that onto
	// task.WorkerID — without preferring it here, a recovered run would
	// re-run router selection from scratch and could land on a different
	// worker than the one runs.worker_id (and any of its already-persisted
	// step rows) already says it's bound to, which confirmRunBinding then
	// correctly refuses as a conflict — stalling the run instead of
	// resuming it on its original worker.
	workerID := task.WorkerID
	if workerID == "" {
		workerID = defaults.Driver.Placement.Worker
	}
	placement := iagent.Placement{
		WorkerID:         workerID,
		Namespace:        ns,
		Infrastructure:   defaults.Driver.Placement.Runtime,
		RequireContainer: pipelineRequiresContainer(&pl),
	}
	if pipelineRequiresNotebook(&pl) {
		placement.RequiredCapabilities = []string{iagent.CapabilityNotebook}
	}
	if label := defaults.Driver.Placement.Label; label != "" {
		placement.Labels = map[string]string{"label": label}
	}
	if placement.WorkerID == "" && len(placement.Labels) == 0 {
		label, err := pipelineRunnerLabel(&pl)
		if err != nil {
			return iagent.Placement{}, err
		}
		if label != "" {
			placement.Labels = map[string]string{"label": label}
		}
	}
	return placement, nil
}

func pipelineRequiresNotebook(pl *pipeline.Pipeline) bool {
	for _, step := range pl.Spec.Steps {
		if step.Run.Type == "notebook" {
			return true
		}
	}
	return false
}

func pipelineRequiresContainer(pl *pipeline.Pipeline) bool {
	if pl.Spec.Defaults != nil && driverHasContainerImage(pl.Spec.Defaults.Driver) {
		return true
	}
	for _, step := range pl.Spec.Steps {
		if driverHasContainerImage(step.Driver) {
			return true
		}
	}
	return false
}

func driverHasContainerImage(driver manifest.DriverSpec) bool {
	return driver.Docker != nil && driver.Docker.Image != "" ||
		driver.K8s != nil && driver.K8s.Image != ""
}

func pipelineRunnerLabel(pl *pipeline.Pipeline) (string, error) {
	var label string
	for _, step := range pl.Spec.Steps {
		if step.Driver.Placement.Label == "" {
			continue
		}
		if label == "" {
			label = step.Driver.Placement.Label
			continue
		}
		if label != step.Driver.Placement.Label {
			return "", fmt.Errorf(
				"pipeline requires multiple runner labels (%q and %q); a run must execute on one worker",
				label,
				step.Driver.Placement.Label,
			)
		}
	}
	return label, nil
}

var _ ExecutionBackend = (*AgentBackend)(nil)
var _ CancelableBackend = (*AgentBackend)(nil)
var _ TaskOwner = (*AgentBackend)(nil)
var _ RunOwner = (*AgentBackend)(nil)
