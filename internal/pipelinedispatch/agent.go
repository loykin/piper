package pipelinedispatch

import (
	"context"
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

	runMu        sync.Mutex
	runAgents    map[string]*pipelineRunAgent // run id -> fixed agent for the whole run
	canceledRuns map[string]struct{}          // run id -> canceled before any binding existed
}

type pipelineRunAgent struct {
	AgentID   string
	Namespace string
	Committed bool
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

// DispatchRun sends an entire run's DAG to its bound worker in a single
// pipeline.run_dispatch RPC — the run-level counterpart to Dispatch's
// per-step pipeline.dispatch. The worker's own scheduler
// (pkg/pipeline/worker/scheduler) then owns every subsequent step's
// execution locally; the master's job here is just choosing and durably
// binding the one worker, then handing off the whole DAG once.
//
// Unlike Dispatch, this is called at most once per run under normal
// operation (Piper.startRun) — Piper.resendUndeliveredRunDispatches is the
// one exception, which may call it again after a master restart for a run
// whose delivery couldn't be confirmed; the worker's Registry.StartRun is
// idempotent specifically so that at-least-once resend is safe. Because
// there is at most one live sender per run, none of Dispatch's
// concurrent-root-step binding coordination (pipelineRunAgent.bindingDone)
// is needed here — this reuses the same runAgents/canceledRuns bookkeeping
// Dispatch and CancelRun already share, just without that extra branch.
func (b *AgentBackend) DispatchRun(ctx context.Context, dispatch proto.RunDispatch) error {
	if b == nil || b.router == nil || b.rpc == nil {
		return fmt.Errorf("pipeline agent backend is not configured")
	}
	placement, err := runDispatchPlacement(&dispatch)
	if err != nil {
		return err
	}

	agentInfo, selectErr := b.router.Reserve(iagent.WorkloadPipeline, placement)
	if selectErr != nil {
		var ambiguous *iagent.AmbiguousInfrastructureError
		if errors.As(selectErr, &ambiguous) {
			// A configuration problem (no driver.placement.runtime to
			// disambiguate multiple infrastructure types), not a transient
			// capacity issue — retrying changes nothing until the pipeline
			// is fixed.
			return &DispatchError{Retryable: false, Err: selectErr}
		}
		// No available worker — retryable so the caller re-attempts after a short delay.
		return &DispatchError{Retryable: true, Err: selectErr}
	}
	runAgent := &pipelineRunAgent{AgentID: agentInfo.ID, Namespace: placement.Namespace}

	b.runMu.Lock()
	if existing, ok := b.runAgents[dispatch.RunID]; ok {
		// A resend (see Piper.resendUndeliveredRunDispatches) racing a still
		// in-flight first attempt for the same run — extremely unlikely
		// (resends only target runs the master believes are stuck), but
		// reuse the existing runAgent rather than clobbering it, so a
		// concurrent CancelRun call doesn't lose track of the one actually
		// in flight.
		runAgent = existing
		b.runMu.Unlock()
	} else {
		b.runAgents[dispatch.RunID] = runAgent
		b.runMu.Unlock()
	}

	unwind := func() {
		b.router.Release(runAgent.AgentID)
		b.runMu.Lock()
		if !runAgent.Committed && b.runAgents[dispatch.RunID] == runAgent {
			delete(b.runAgents, dispatch.RunID)
		}
		b.runMu.Unlock()
	}

	if err := b.confirmRunBinding(ctx, dispatch.ProjectID, dispatch.RunID, runAgent.AgentID); err != nil {
		unwind()
		return err
	}

	b.runMu.Lock()
	_, tombstoned := b.canceledRuns[dispatch.RunID]
	b.runMu.Unlock()
	if tombstoned {
		unwind()
		b.runMu.Lock()
		delete(b.canceledRuns, dispatch.RunID)
		b.runMu.Unlock()
		return fmt.Errorf("pipeline agent dispatch: run %s was canceled before dispatch", dispatch.RunID)
	}

	dispatchCopy := dispatch
	if b.podPolicies != nil {
		policy, pErr := b.podPolicies.Get(ctx, runAgent.AgentID)
		if pErr != nil {
			slog.Warn("pipeline: pod policy lookup failed, proceeding without policy",
				"worker_id", runAgent.AgentID, "err", pErr)
		} else if policy != nil {
			if merged, mErr := applyPodPolicyToPipelineYAML(dispatchCopy.PipelineYAML, policy.PodTemplate); mErr == nil {
				dispatchCopy.PipelineYAML = merged
			} else {
				slog.Warn("pipeline: pod policy merge failed, using original pipeline",
					"worker_id", runAgent.AgentID, "err", mErr)
			}
		}
	}
	if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineRunDispatch, &dispatchCopy, nil); err != nil {
		unwind()
		// Worker capacity refusal: BusyErrorMarker is embedded in the error string
		// because gRPC serialises the error to a plain string on the wire.
		if strings.Contains(err.Error(), iagent.BusyErrorMarker) {
			return &DispatchError{Retryable: true, Err: err}
		}
		return fmt.Errorf("pipeline agent dispatch: %w", err)
	}

	b.runMu.Lock()
	runAgent.Committed = true
	// Re-check canceledRuns after the dispatch RPC has actually gone out,
	// not just before — see Dispatch's identical re-check for the full
	// rationale (SendRPC is a network call CancelRun can land inside of).
	_, tombstonedAfterSend := b.canceledRuns[dispatch.RunID]
	if tombstonedAfterSend {
		delete(b.canceledRuns, dispatch.RunID)
	}
	b.runMu.Unlock()
	if tombstonedAfterSend {
		if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineCancelRun, map[string]any{
			"run_id":    dispatch.RunID,
			"namespace": runAgent.Namespace,
		}, nil); err != nil {
			slog.Warn("pipeline: best-effort cancel after run dispatch raced a cancel failed", "run_id", dispatch.RunID, "err", err)
		}
		return fmt.Errorf("pipeline agent dispatch: run %s was canceled while its dispatch RPC was in flight", dispatch.RunID)
	}
	return nil
}

// confirmRunBinding persists runs.worker_id for runID as workerID before a
// dispatch RPC (per-step or run-level) ever sends the workload to that
// worker. This ordering matters: once dispatched, the worker can report
// back (step_upsert, run_finalize) essentially immediately — for a fast
// single-step run, often before this function would have returned if it ran
// concurrently with (or after) the dispatch RPC instead of strictly before
// it. Those handlers treat runs.worker_id as their authorization root (see
// pipeline_db_handlers.go), so it must already be durable by the time the
// worker could possibly call them, not merely "eventually consistent."
func (b *AgentBackend) confirmRunBinding(ctx context.Context, projectID, runID, workerID string) error {
	if b.runRepo == nil {
		return nil
	}
	applied, err := b.runRepo.SetWorkerID(ctx, projectID, runID, workerID)
	if err != nil {
		return &DispatchError{Retryable: true, Err: fmt.Errorf("confirm run binding: %w", err)}
	}
	if applied {
		return nil
	}
	// Not applied: the row already had a worker_id — most likely this is a
	// retried dispatch call (e.g. after a transient failure earlier in this
	// same function, or a master-restart resend — see
	// Piper.resendUndeliveredRunDispatches) re-confirming the same binding.
	// Read it back to tell that apart from a genuine conflict (the run bound
	// to a *different* worker), which must not be allowed to proceed
	// silently.
	existing, getErr := b.runRepo.Get(ctx, projectID, runID)
	if getErr != nil {
		return &DispatchError{Retryable: true, Err: fmt.Errorf("confirm run binding: read back: %w", getErr)}
	}
	if existing == nil || existing.WorkerID != workerID {
		return &DispatchError{Retryable: false, Err: fmt.Errorf("pipeline agent dispatch: run %s is already bound to a different worker", runID)}
	}
	return nil
}

// IsTracking reports whether this AgentBackend instance already has (or is
// in the middle of establishing) a binding for runID. Used by
// Piper.resendUndeliveredRunDispatches to skip runs this same master
// process already dispatched — without this, every periodic sweep tick
// would re-send pipeline.run_dispatch for every currently-running run,
// forever, instead of only for runs left over from before a master
// restart. A run vanishes from tracking once it finalizes (ReleaseRun).
func (b *AgentBackend) IsTracking(runID string) bool {
	b.runMu.Lock()
	defer b.runMu.Unlock()
	_, ok := b.runAgents[runID]
	return ok
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

// runDispatchPlacement resolves placement from a RunDispatch's PipelineYAML —
// the run-level dispatch has no per-step manifest override to consider,
// since every step of a run always shares the same worker (see AGENTS.md's
// Worker Assignment invariant), so only the pipeline's top-level defaults
// matter here.
func runDispatchPlacement(dispatch *proto.RunDispatch) (iagent.Placement, error) {
	if dispatch == nil {
		return iagent.Placement{}, fmt.Errorf("run dispatch is required")
	}
	pl, err := pipeline.Parse([]byte(dispatch.PipelineYAML))
	if err != nil {
		return iagent.Placement{}, fmt.Errorf("parse pipeline yaml: %w", err)
	}
	var defaults pipeline.PipelineDefaults
	if pl.Spec.Defaults != nil {
		defaults = *pl.Spec.Defaults
	}
	var ns string
	if defaults.Driver.K8s != nil {
		ns = defaults.Driver.K8s.Namespace
	}
	// dispatch.WorkerID (set only for a resend of an already-bound run —
	// see RunDispatch's doc comment) takes priority over the manifest's own
	// defaults.driver.placement.worker, mirroring taskPlacement's identical
	// priority for task.WorkerID on recovery.
	workerID := dispatch.WorkerID
	if workerID == "" {
		workerID = defaults.Driver.Placement.Worker
	}
	placement := iagent.Placement{
		WorkerID:         workerID,
		Namespace:        ns,
		Infrastructure:   defaults.Driver.Placement.Runtime,
		RequireContainer: pipelineRequiresContainer(pl),
	}
	if pipelineRequiresNotebook(pl) {
		placement.RequiredCapabilities = []string{iagent.CapabilityNotebook}
	}
	if label := defaults.Driver.Placement.Label; label != "" {
		placement.Labels = map[string]string{"label": label}
	}
	if placement.WorkerID == "" && len(placement.Labels) == 0 {
		label, err := pipelineRunnerLabel(pl)
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

var _ CancelableBackend = (*AgentBackend)(nil)
var _ RunOwner = (*AgentBackend)(nil)
var _ RunDispatchBackend = (*AgentBackend)(nil)
