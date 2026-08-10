package piper

import (
	"context"
	"fmt"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/internal/grpcagent"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

// registerPipelineDBHandlers wires the worker-initiated DB access interface
// described in docs/backend/develop.md's State Ownership section: each
// worker's own scheduler (pkg/pipeline/worker/scheduler) calls these through
// grpcagent.Client.SendRequest to report step/run state as it owns DAG
// promotion, retry, and timeout locally — the master no longer decides or
// pushes those state changes down itself.
//
// Each handler is deliberately thin: resolve the acting worker's identity
// from the authenticated tunnel connection (never from the payload), then
// hand off straight to the existing DB-level CAS methods — no additional
// Go-level scheduling judgment here.
//
// events and onRunSuccess replicate what the old in-memory Queue used to do
// on every finalize: without them, nothing publishes "run.completed"
// (waitForRunCompleted blocks forever) or invokes the on_success.deploy
// auto-redeploy hook. backend (optional; a pipelinedispatch.RunOwner such as
// AgentBackend) is released via ReleaseRun when a run reaches a terminal
// status, so the backend's own in-memory run binding doesn't leak for runs
// that finish normally instead of being explicitly canceled.
func registerPipelineDBHandlers(srv *grpcagent.Server, runRepo run.Repository, stepRepo run.StepRepository, events event.Publisher, onRunSuccess func(ctx context.Context, runID string, pl *pipeline.Pipeline), backend runReleaser) {
	_ = grpcagent.RegisterJSON(srv.Dispatcher(), iagent.MethodPipelineStepUpsert, func(ctx context.Context, req run.Step) (any, error) {
		agentID := grpcagent.RequestAgentID(ctx)
		if agentID == "" {
			return nil, fmt.Errorf("pipeline.step_upsert: no authenticated worker identity")
		}
		if req.ProjectID == "" || req.RunID == "" || req.StepName == "" {
			return nil, fmt.Errorf("pipeline.step_upsert: project_id, run_id, and step_name are required")
		}
		switch req.Status {
		case run.StepStatusRunning, run.StepStatusDone, run.StepStatusFailed, run.StepStatusSkipped, run.StepStatusCanceled:
		default:
			// An unvalidated status (e.g. a typo, or a future caller
			// forgetting to set it) would otherwise persist as-is: worse
			// than a rejected request, it would make ListNonTerminalByWorker
			// treat the step as permanently non-terminal (it only excludes
			// the exact terminal literals above), leaving it stuck in
			// recovery queries forever with no path to resolution.
			return nil, fmt.Errorf("pipeline.step_upsert: status must be one of running/done/failed/skipped/canceled, got %q", req.Status)
		}
		// A run's worker_id (set by pipelinedispatch.AgentBackend.Dispatch
		// before it ever sends the workload to that worker — see
		// confirmRunBinding) is the authorization root for every subsequent
		// worker RPC concerning that run. Without checking it here, worker-2
		// could upsert steps for a run bound to worker-1 just by sending its
		// project_id/run_id/step_name — WorkerID being overwritten from the
		// authenticated identity (below) only stops it from *claiming* to be
		// worker-1, not from touching worker-1's run at all.
		existing, err := runRepo.Get(ctx, req.ProjectID, req.RunID)
		if err != nil {
			return nil, err
		}
		if existing == nil || existing.WorkerID != agentID {
			return nil, fmt.Errorf("pipeline.step_upsert: run %s/%s is not bound to this worker", req.ProjectID, req.RunID)
		}
		switch existing.Status {
		case run.StatusSuccess, run.StatusFailed, run.StatusCanceled:
			// The run itself already finished — UpsertCAS's own attempts/
			// status guard only protects one step row at a time and knows
			// nothing about the run it belongs to, so a higher-attempt
			// report for a step of an already-terminal run would otherwise
			// still go through and resurrect it as non-terminal.
			return nil, fmt.Errorf("pipeline.step_upsert: run %s/%s is already terminal (%s)", req.ProjectID, req.RunID, existing.Status)
		}
		// Never trust a worker_id the payload might carry — see
		// grpcagent.RequestAgentID and the worker_push.go precedent this
		// mirrors.
		req.WorkerID = agentID
		applied, err := stepRepo.UpsertCAS(ctx, &req)
		if err != nil {
			return nil, err
		}
		if applied && events != nil {
			fields := map[string]any{"run_id": req.RunID, "step": req.StepName, "attempts": req.Attempts}
			if req.Error != "" {
				fields["error"] = req.Error
			}
			events.Publish(event.New(req.ProjectID, "step."+req.Status, fields))
		}
		return stepUpsertResponse{Applied: applied}, nil
	})

	_ = grpcagent.RegisterJSON(srv.Dispatcher(), iagent.MethodPipelineRunFinalize, func(ctx context.Context, req runFinalizeRequest) (any, error) {
		agentID := grpcagent.RequestAgentID(ctx)
		if agentID == "" {
			return nil, fmt.Errorf("pipeline.run_finalize: no authenticated worker identity")
		}
		if req.ProjectID == "" || req.ID == "" {
			return nil, fmt.Errorf("pipeline.run_finalize: project_id and id are required")
		}
		switch req.Status {
		case run.StatusSuccess, run.StatusFailed, run.StatusCanceled:
		default:
			return nil, fmt.Errorf("pipeline.run_finalize: status must be one of success/failed/canceled, got %q", req.Status)
		}
		// FinalizeStatusCAS alone only guards against double-finalizing (a
		// concurrency concern); it says nothing about *which* worker is
		// allowed to finalize *which* run. A run's worker_id (confirmed
		// durable before dispatch — see
		// pipelinedispatch.AgentBackend.confirmRunBinding) is the
		// authorization check for that. An empty/missing worker_id is never
		// an acceptable bypass here: confirmRunBinding guarantees it's set
		// before the worker could possibly have anything to finalize.
		existing, err := runRepo.Get(ctx, req.ProjectID, req.ID)
		if err != nil {
			return nil, err
		}
		if existing == nil || existing.WorkerID != agentID {
			return nil, fmt.Errorf("pipeline.run_finalize: run %s/%s is not bound to this worker", req.ProjectID, req.ID)
		}
		applied, err := runRepo.FinalizeStatusCAS(ctx, req.ProjectID, req.ID, req.Status, req.EndedAt)
		if err != nil {
			return nil, err
		}
		if applied {
			if backend != nil {
				backend.ReleaseRun(req.ID)
			}
			if events != nil {
				eventType := "run.completed"
				if req.Status == run.StatusCanceled {
					eventType = "run.canceled"
				}
				events.Publish(event.New(req.ProjectID, eventType, map[string]any{"run_id": req.ID, "status": req.Status}))
			}
			if req.Status == run.StatusSuccess && onRunSuccess != nil && existing.PipelineYAML != "" {
				if pl, perr := pipeline.Parse([]byte(existing.PipelineYAML)); perr == nil {
					// Detached context: this handler's ctx is scoped to the
					// inbound RPC and must not cancel the redeploy it triggers.
					go onRunSuccess(project.WithContext(context.Background(), project.Context{ID: req.ProjectID}), req.ID, pl)
				}
			}
		}
		return runFinalizeResponse{Applied: applied}, nil
	})

	_ = grpcagent.RegisterJSON(srv.Dispatcher(), iagent.MethodPipelineWorkerRecoveryQuery, func(ctx context.Context, _ workerRecoveryQueryRequest) (any, error) {
		agentID := grpcagent.RequestAgentID(ctx)
		if agentID == "" {
			return nil, fmt.Errorf("pipeline.worker_recovery_query: no authenticated worker identity")
		}
		// Always the caller's own authenticated identity — never a
		// caller-supplied worker_id — so a worker can only ever discover its
		// own non-terminal work, not another worker's.
		steps, err := stepRepo.ListNonTerminalByWorker(ctx, agentID)
		if err != nil {
			return nil, err
		}
		// Group by (ProjectID, RunID) and attach each distinct run's own row
		// (pipeline_yaml/params_json/cancel_requested_at) — a restarting
		// worker's scheduler needs the full DAG and params to resume, not
		// just bare step rows, and needs to know about a cancel that arrived
		// while it was down. Preserves first-seen run order for determinism.
		type key struct{ projectID, runID string }
		byRun := make(map[key][]*run.Step, len(steps))
		var order []key
		for _, s := range steps {
			k := key{s.ProjectID, s.RunID}
			if _, seen := byRun[k]; !seen {
				order = append(order, k)
			}
			byRun[k] = append(byRun[k], s)
		}
		resp := WorkerRecoveryResponse{Runs: make([]RecoveredRun, 0, len(order))}
		for _, k := range order {
			r, err := runRepo.Get(ctx, k.projectID, k.runID)
			if err != nil {
				return nil, err
			}
			if r == nil {
				// Step rows outlived their run row (shouldn't normally
				// happen — DeleteByRun/Delete are meant to go together) —
				// skip rather than hand the worker steps it can't attach a
				// DAG to.
				continue
			}
			switch r.Status {
			case run.StatusSuccess, run.StatusFailed, run.StatusCanceled:
				// The run finished (e.g. another path finalized it) between
				// ListNonTerminalByWorker's read and this Get — its step
				// rows are stale reads of a run that's no longer this
				// worker's responsibility to resume.
				continue
			}
			resp.Runs = append(resp.Runs, RecoveredRun{
				Run:             r,
				Steps:           byRun[k],
				CancelRequested: r.CancelRequestedAt != nil,
			})
		}
		return resp, nil
	})
}

// runReleaser is satisfied by pipelinedispatch.RunOwner (AgentBackend) — see
// registerPipelineDBHandlers's backend param doc.
type runReleaser interface {
	ReleaseRun(runID string)
}

// WorkerRecoveryResponse is the pipeline.worker_recovery_query response: every
// non-terminal run currently bound to the calling worker, enriched enough for
// its local scheduler to reconstruct a full RunScheduler on restart.
type WorkerRecoveryResponse struct {
	Runs []RecoveredRun `json:"runs"`
}

// RecoveredRun pairs a run's durable row (pipeline_yaml, params_json, and
// cancel intent) with its non-terminal step rows.
type RecoveredRun struct {
	Run   *run.Run    `json:"run"`
	Steps []*run.Step `json:"steps"`
	// CancelRequested mirrors Run.CancelRequestedAt != nil — a cancel that
	// arrived while this worker was unreachable and must be applied
	// immediately during recovery instead of resuming the run normally.
	CancelRequested bool `json:"cancel_requested"`
}

type stepUpsertResponse struct {
	Applied bool `json:"applied"`
}

type runFinalizeRequest struct {
	ProjectID string     `json:"project_id"`
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

type runFinalizeResponse struct {
	Applied bool `json:"applied"`
}

// workerRecoveryQueryRequest is currently empty — the acting worker is
// always the authenticated tunnel identity (RequestAgentID), never a
// caller-supplied field — but kept as a named type so a future field (e.g. a
// capability filter) doesn't require a wire-format break.
type workerRecoveryQueryRequest struct{}
