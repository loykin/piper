package piper

import (
	"context"
	"testing"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

func TestSweepStaleWorkerBoundRunsForceFinalizesUnreachableWorker(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID
	p.SetBackend(&fakeRunDispatchBackend{})

	const runID = "run-stale-unreachable"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "stale-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC().Add(-2 * staleWorkerGrace), // no heartbeat ever — StartedAt is the fallback reference
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if applied, err := p.repos.Run.SetWorkerID(ctx, projectID, runID, "worker-gone"); err != nil || !applied {
		t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
	}
	// worker-gone is deliberately never registered in p.agentRegistry.

	p.sweepStaleWorkerBoundRuns(ctx)

	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusFailed {
		t.Fatalf("run status = %+v, want failed (bound worker unreachable and stale)", got)
	}
}

func TestSweepStaleWorkerBoundRunsFinalizesAsCanceledWhenCancelWasRequested(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID
	p.SetBackend(&fakeRunDispatchBackend{})

	const runID = "run-stale-cancel-requested"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "stale-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC().Add(-2 * staleWorkerGrace),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if applied, err := p.repos.Run.SetWorkerID(ctx, projectID, runID, "worker-gone"); err != nil || !applied {
		t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
	}
	if applied, err := p.repos.Run.SetCancelRequested(ctx, projectID, runID); err != nil || !applied {
		t.Fatalf("SetCancelRequested: applied=%v err=%v", applied, err)
	}

	p.sweepStaleWorkerBoundRuns(ctx)

	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusCanceled {
		t.Fatalf("run status = %+v, want canceled (cancel was pending when the worker turned out to be gone)", got)
	}
}

func TestSweepStaleWorkerBoundRunsSkipsFreshHeartbeat(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID
	p.SetBackend(&fakeRunDispatchBackend{})

	const runID = "run-fresh-heartbeat"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "stale-pipeline",
		Status:       run.StatusRunning,
		// StartedAt itself is old/stale, but a fresh heartbeat below must
		// override it as the liveness reference.
		StartedAt: time.Now().UTC().Add(-2 * staleWorkerGrace),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if applied, err := p.repos.Run.SetWorkerID(ctx, projectID, runID, "worker-gone"); err != nil || !applied {
		t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
	}
	if err := p.repos.Run.TouchWorkerLastSeen(ctx, "worker-gone", []string{runID}); err != nil {
		t.Fatalf("TouchWorkerLastSeen: %v", err)
	}

	p.sweepStaleWorkerBoundRuns(ctx)

	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusRunning {
		t.Fatalf("run status = %+v, want still running (heartbeat is fresh)", got)
	}
}

func TestSweepStaleWorkerBoundRunsSkipsConnectedWorker(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID
	p.SetBackend(&fakeRunDispatchBackend{})

	const runID = "run-worker-still-connected"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "stale-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC().Add(-2 * staleWorkerGrace),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if applied, err := p.repos.Run.SetWorkerID(ctx, projectID, runID, "worker-still-here"); err != nil || !applied {
		t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
	}
	p.agentRegistry.Register(iagent.Info{
		ID:             "worker-still-here",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})

	p.sweepStaleWorkerBoundRuns(ctx)

	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusRunning {
		t.Fatalf("run status = %+v, want still running (worker is still connected, only its DB heartbeat is stale)", got)
	}
}

func TestSweepStaleWorkerBoundRunsSkipsUndispatchedRun(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID
	p.SetBackend(&fakeRunDispatchBackend{})

	const runID = "run-never-dispatched"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "stale-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC().Add(-2 * staleWorkerGrace),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	// No SetWorkerID call — this run was never (even partially) dispatched.

	p.sweepStaleWorkerBoundRuns(ctx)

	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusRunning {
		t.Fatalf("run status = %+v, want still running (never bound to a worker — not this sweep's concern)", got)
	}
}
