package pipelinedispatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/proto"
)

// minimalRunDispatchYAML is a valid single-step pipeline — pipeline.Parse
// (used by runDispatchPlacement) validates the manifest, unlike Dispatch's
// tests which can get away with json.Marshal-ing a bare pipeline.Pipeline{}
// since taskPlacement only unmarshals, never validates.
const minimalRunDispatchYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: dispatchrun-test
spec:
  steps:
    - name: a
      run:
        command: ["true"]
`

func gpuLabelRunDispatchYAML() string {
	return `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: dispatchrun-test
spec:
  defaults:
    driver:
      placement:
        label: gpu
  steps:
    - name: a
      run:
        command: ["true"]
`
}

func TestAgentBackendDispatchRunUsesPipelinePlacement(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "agent-1",
		Infrastructure: iagent.InfrastructureK8s,
		Labels:         map[string]string{"label": "gpu"},
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: gpuLabelRunDispatchYAML()}
	if err := backend.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun returned error: %v", err)
	}
	calls := rpc.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d RPC calls, want 1: %#v", len(calls), calls)
	}
	if calls[0].AgentID != "agent-1" {
		t.Fatalf("agent id = %q", calls[0].AgentID)
	}
	if calls[0].Method != iagent.MethodPipelineRunDispatch {
		t.Fatalf("method = %q, want %q", calls[0].Method, iagent.MethodPipelineRunDispatch)
	}
	sent, ok := calls[0].Payload.(*proto.RunDispatch)
	if !ok {
		t.Fatalf("payload type = %T, want *proto.RunDispatch", calls[0].Payload)
	}
	if sent.RunID != "run-1" {
		t.Fatalf("payload RunID = %q", sent.RunID)
	}
}

func TestAgentBackendDispatchRunConfirmsBindingBeforeSendingRPC(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	repo := newStubRunRepoForBinding()
	rpc := &orderCheckingRunDispatchRPC{repo: repo}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, repo)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: minimalRunDispatchYAML}
	if err := backend.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun returned error: %v", err)
	}
	if !rpc.checked {
		t.Fatal("run_dispatch RPC was never sent")
	}
	if !rpc.boundBeforeSend {
		t.Fatal("runs.worker_id was not yet confirmed at the moment the run_dispatch RPC was sent")
	}
	if repo.setCalls != 1 {
		t.Fatalf("SetWorkerID called %d times, want exactly 1", repo.setCalls)
	}
}

type orderCheckingRunDispatchRPC struct {
	repo            *stubRunRepoForBinding
	checked         bool
	boundBeforeSend bool
}

func (r *orderCheckingRunDispatchRPC) SendRPC(ctx context.Context, agentID, method string, payload any, _ any) error {
	if method == iagent.MethodPipelineRunDispatch {
		dispatch := payload.(*proto.RunDispatch)
		existing, _ := r.repo.Get(ctx, dispatch.ProjectID, dispatch.RunID)
		r.checked = true
		r.boundBeforeSend = existing != nil && existing.WorkerID == agentID
	}
	return nil
}

func TestAgentBackendDispatchRunFailsWhenRunAlreadyBoundToDifferentWorker(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	repo := newStubRunRepoForBinding()
	repo.bound[runBindingKey("proj-1", "run-1")] = "some-other-worker"
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, repo)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: minimalRunDispatchYAML}
	err := backend.DispatchRun(context.Background(), dispatch)
	if err == nil {
		t.Fatal("DispatchRun succeeded for a run already bound to a different worker")
	}
	var de *DispatchError
	if !errors.As(err, &de) {
		t.Fatalf("error = %#v, want a *DispatchError", err)
	}
	if de.Retryable {
		t.Fatal("a binding conflict must not be retryable")
	}
	if calls := rpc.snapshot(); len(calls) != 0 {
		t.Fatalf("expected no dispatch RPC to be sent, got %#v", calls)
	}
	if backend.IsTracking("run-1") {
		t.Fatal("IsTracking still true after a failed DispatchRun — runAgents entry was not cleaned up")
	}
}

func TestAgentBackendDispatchRunTombstonedBeforeSendAborts(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	// CancelRun before any DispatchRun call for this run tombstones it.
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: minimalRunDispatchYAML}
	err := backend.DispatchRun(context.Background(), dispatch)
	if err == nil {
		t.Fatal("DispatchRun succeeded for a run canceled before dispatch")
	}
	if calls := rpc.snapshot(); len(calls) != 0 {
		t.Fatalf("a dispatch RPC was sent for a run canceled before dispatch: %#v", calls)
	}
}

func TestAgentBackendDispatchRunCancelDuringSendRPCTriggersCompensatingCancel(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &gatedRunDispatchRPC{entered: make(chan struct{}, 1), gate: make(chan struct{})}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: minimalRunDispatchYAML}
	errCh := make(chan error, 1)
	go func() {
		errCh <- backend.DispatchRun(context.Background(), dispatch)
	}()

	select {
	case <-rpc.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("run_dispatch RPC never started")
	}

	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	close(rpc.gate)

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("DispatchRun succeeded despite a cancel landing during its in-flight SendRPC")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DispatchRun never returned")
	}

	calls := rpc.snapshot()
	if len(calls) != 2 {
		t.Fatalf("got %d RPC calls, want 2 (dispatch + compensating cancel): %#v", len(calls), calls)
	}
	if calls[0].Method != iagent.MethodPipelineRunDispatch {
		t.Fatalf("first call method = %q, want %q", calls[0].Method, iagent.MethodPipelineRunDispatch)
	}
	if calls[1].Method != iagent.MethodPipelineCancelRun {
		t.Fatalf("second call method = %q, want %q (compensating cancel)", calls[1].Method, iagent.MethodPipelineCancelRun)
	}
}

type gatedRunDispatchRPC struct {
	mu      sync.Mutex
	calls   []pipelineAgentRPCCall
	entered chan struct{}
	gate    chan struct{}
}

func (r *gatedRunDispatchRPC) SendRPC(_ context.Context, agentID, method string, payload any, _ any) error {
	r.mu.Lock()
	r.calls = append(r.calls, pipelineAgentRPCCall{AgentID: agentID, Method: method, Payload: payload})
	r.mu.Unlock()
	if method == iagent.MethodPipelineRunDispatch {
		r.entered <- struct{}{}
		<-r.gate
	}
	return nil
}

func (r *gatedRunDispatchRPC) snapshot() []pipelineAgentRPCCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pipelineAgentRPCCall(nil), r.calls...)
}

func TestAgentBackendIsTrackingReflectsRunAgentLifecycle(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	if backend.IsTracking("run-1") {
		t.Fatal("IsTracking true before any dispatch")
	}
	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: minimalRunDispatchYAML}
	if err := backend.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun: %v", err)
	}
	if !backend.IsTracking("run-1") {
		t.Fatal("IsTracking false right after a successful DispatchRun")
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if backend.IsTracking("run-1") {
		t.Fatal("IsTracking still true after CancelRun released the run")
	}
}
