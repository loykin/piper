package piper

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

// fakeRunDispatchBackend is a minimal pipelinedispatch.RunDispatchBackend
// test double, used in place of the real AgentBackend so these tests can
// observe exactly what resendUndeliveredRunDispatches sends without needing
// a real connected worker.
type fakeRunDispatchBackend struct {
	mu            sync.Mutex
	tracked       map[string]bool
	dispatchCalls []proto.RunDispatch
	dispatchErr   error
	cancelCalls   []string
	cancelErr     error
}

func (b *fakeRunDispatchBackend) Dispatch(context.Context, *proto.Task) error {
	return fmt.Errorf("not implemented")
}

func (b *fakeRunDispatchBackend) DispatchRun(_ context.Context, dispatch proto.RunDispatch) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dispatchCalls = append(b.dispatchCalls, dispatch)
	if b.dispatchErr != nil {
		return b.dispatchErr
	}
	if b.tracked == nil {
		b.tracked = make(map[string]bool)
	}
	b.tracked[dispatch.RunID] = true
	return nil
}

func (b *fakeRunDispatchBackend) IsTracking(runID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tracked[runID]
}

func (b *fakeRunDispatchBackend) calls() []proto.RunDispatch {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]proto.RunDispatch(nil), b.dispatchCalls...)
}

func (b *fakeRunDispatchBackend) CancelRun(_ context.Context, runID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelCalls = append(b.cancelCalls, runID)
	return b.cancelErr
}

func (b *fakeRunDispatchBackend) cancels() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.cancelCalls...)
}

const resendTestPipelineYAML = "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: resend-pipeline\nspec:\n  steps:\n  - name: only\n    run:\n      type: command\n      command: [\"true\"]\n"

func TestResendUndeliveredRunDispatchesSkipsTrackedRun(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID

	const runID = "run-already-tracked"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "resend-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
		PipelineYAML: resendTestPipelineYAML,
		ParamsJSON:   "{}",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	backend := &fakeRunDispatchBackend{tracked: map[string]bool{runID: true}}
	p.SetBackend(backend)

	p.resendUndeliveredRunDispatches(ctx, backend)

	if calls := backend.calls(); len(calls) != 0 {
		t.Fatalf("DispatchRun called %d times for an already-tracked run, want 0: %#v", len(calls), calls)
	}
}

func TestResendUndeliveredRunDispatchesResendsUntrackedRunningRun(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID

	const runID = "run-untracked-running"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "resend-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
		PipelineYAML: resendTestPipelineYAML,
		ParamsJSON:   "{}",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	// Simulate a run already durably bound from before a master restart —
	// resend must force placement back onto this exact worker.
	if applied, err := p.repos.Run.SetWorkerID(ctx, projectID, runID, "worker-1"); err != nil || !applied {
		t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
	}

	backend := &fakeRunDispatchBackend{}
	p.SetBackend(backend)

	p.resendUndeliveredRunDispatches(ctx, backend)

	calls := backend.calls()
	if len(calls) != 1 {
		t.Fatalf("DispatchRun called %d times, want 1: %#v", len(calls), calls)
	}
	if calls[0].RunID != runID {
		t.Fatalf("resent RunID = %q, want %q", calls[0].RunID, runID)
	}
	if calls[0].WorkerID != "worker-1" {
		t.Fatalf("resent WorkerID = %q, want %q (must force placement onto the already-bound worker)", calls[0].WorkerID, "worker-1")
	}
	if calls[0].ProjectID != projectID {
		t.Fatalf("resent ProjectID = %q, want %q", calls[0].ProjectID, projectID)
	}
}

func TestResendUndeliveredRunDispatchesMarksFailedWhenPipelineYAMLMissing(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID

	const runID = "run-no-yaml"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "resend-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
		// PipelineYAML deliberately empty.
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	backend := &fakeRunDispatchBackend{}
	p.SetBackend(backend)

	p.resendUndeliveredRunDispatches(ctx, backend)

	if calls := backend.calls(); len(calls) != 0 {
		t.Fatalf("DispatchRun called %d times for a run with no pipeline_yaml, want 0: %#v", len(calls), calls)
	}
	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusFailed {
		t.Fatalf("run status = %+v, want failed", got)
	}
}

func TestResendUndeliveredRunDispatchesLeavesRunRunningOnDispatchFailure(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID

	const runID = "run-dispatch-fails"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "resend-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
		PipelineYAML: resendTestPipelineYAML,
		ParamsJSON:   "{}",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	backend := &fakeRunDispatchBackend{dispatchErr: fmt.Errorf("no available worker")}
	p.SetBackend(backend)

	p.resendUndeliveredRunDispatches(ctx, backend)

	if calls := backend.calls(); len(calls) != 1 {
		t.Fatalf("DispatchRun called %d times, want 1: %#v", len(calls), calls)
	}
	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	// Left running — must be retried on the next sweep, not silently
	// abandoned or incorrectly marked terminal on a transient failure.
	if got == nil || got.Status != run.StatusRunning {
		t.Fatalf("run status = %+v, want still running after a failed dispatch attempt", got)
	}
}

func TestReconcileInterruptedRunsUsesResendPathForRunDispatchBackend(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID

	const runID = "run-via-reconcile"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "resend-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
		PipelineYAML: resendTestPipelineYAML,
		ParamsJSON:   "{}",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	backend := &fakeRunDispatchBackend{}
	p.SetBackend(backend)

	p.reconcileInterruptedRuns(ctx)

	if calls := backend.calls(); len(calls) != 1 {
		t.Fatalf("DispatchRun called %d times via reconcileInterruptedRuns, want 1: %#v", len(calls), calls)
	}
}
