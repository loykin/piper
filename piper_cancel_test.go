package piper

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

func TestCancelRunFinalizesDirectlyWhenNeverDispatched(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	projectID := project.DefaultID
	ctx := project.WithContext(context.Background(), project.Context{ID: projectID})

	backend := &fakeRunDispatchBackend{}
	p.SetBackend(backend)

	const runID = "run-never-dispatched-cancel"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "cancel-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := p.CancelRun(ctx, runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	if calls := backend.cancels(); len(calls) != 0 {
		t.Fatalf("backend.CancelRun called %d times for a never-dispatched run, want 0: %#v", len(calls), calls)
	}
	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusCanceled {
		t.Fatalf("run status = %+v, want canceled", got)
	}
}

func TestCancelRunPersistsIntentAndRelaysWhenDispatched(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	projectID := project.DefaultID
	ctx := project.WithContext(context.Background(), project.Context{ID: projectID})

	backend := &fakeRunDispatchBackend{}
	p.SetBackend(backend)

	const runID = "run-dispatched-cancel"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "cancel-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if applied, err := p.repos.Run.SetWorkerID(ctx, projectID, runID, "worker-1"); err != nil || !applied {
		t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
	}

	if err := p.CancelRun(ctx, runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	if calls := backend.cancels(); len(calls) != 1 || calls[0] != runID {
		t.Fatalf("backend.CancelRun calls = %#v, want exactly [%s]", calls, runID)
	}
	// The run itself must NOT be finalized by the master directly — the
	// worker's own scheduler owns that decision under this model.
	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusRunning {
		t.Fatalf("run status = %+v, want still running (worker owns finalization, not the master)", got)
	}
	if got.CancelRequestedAt == nil {
		t.Fatal("CancelRequestedAt is nil — cancel intent was not durably recorded")
	}
}

func TestCancelRunPersistsIntentDespiteRelayFailure(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	projectID := project.DefaultID
	ctx := project.WithContext(context.Background(), project.Context{ID: projectID})

	backend := &fakeRunDispatchBackend{cancelErr: fmt.Errorf("worker unreachable")}
	p.SetBackend(backend)

	const runID = "run-cancel-relay-fails"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "cancel-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if applied, err := p.repos.Run.SetWorkerID(ctx, projectID, runID, "worker-1"); err != nil || !applied {
		t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
	}

	// Must not surface the relay failure to the caller — the intent is
	// durably recorded regardless, and will be consumed by the staleness
	// sweep or a future reconnect.
	if err := p.CancelRun(ctx, runID); err != nil {
		t.Fatalf("CancelRun returned an error despite durably recording cancel intent: %v", err)
	}

	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.CancelRequestedAt == nil {
		t.Fatal("CancelRequestedAt is nil — cancel intent was not durably recorded despite the relay failing")
	}
	if got.Status != run.StatusRunning {
		t.Fatalf("run status = %q, want still running", got.Status)
	}
}
