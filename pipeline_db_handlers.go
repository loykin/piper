package piper

import (
	"context"
	"fmt"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/grpcagent"
	"github.com/loykin/piper/pkg/pipeline/run"
)

// registerPipelineDBHandlers wires the worker-initiated DB access interface
// described in docs/backend/develop.md's State Ownership section: a worker
// calls these through grpcagent.Client.SendRequest instead of the master
// deciding and pushing state changes down. Nothing in production calls these
// yet (see the Phase 2 worker-side scheduler design) — this registers real,
// tested endpoints for when that lands, rather than leaving the transport
// wired to "method ... is not supported."
//
// Each handler is deliberately thin: resolve the acting worker's identity
// from the authenticated tunnel connection (never from the payload), then
// hand off straight to the existing DB-level CAS methods — no additional
// Go-level scheduling judgment here, matching how worker_push.go's existing
// task-result path already treats the DB row, not in-memory state, as the
// source of truth.
func registerPipelineDBHandlers(srv *grpcagent.Server, runRepo run.Repository, stepRepo run.StepRepository) {
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
		//
		// NOTE: this response is intentionally minimal — []run.Step rows
		// only (status/attempts/timestamps), not enough on its own to
		// rebuild a full scheduler: no pipeline YAML/DAG, run params, or
		// storage location, and the run each step belongs to isn't even
		// deduplicated for the caller. That's fine for what exists today
		// (nothing calls this endpoint in production yet — see
		// registerPipelineDBHandlers), but a real worker-side scheduler
		// will need this enriched, most plausibly by grouping results by
		// RunID and joining runRepo.Get for each distinct run to attach its
		// pipeline_yaml/params_json. Do this when that scheduler is
		// actually designed, against its real requirements — not
		// speculatively now.
		return stepRepo.ListNonTerminalByWorker(ctx, agentID)
	})
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
