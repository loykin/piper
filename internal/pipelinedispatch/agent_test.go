package pipelinedispatch

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
)

type recordingPipelineAgentRPC struct {
	mu      sync.Mutex
	calls   []pipelineAgentRPCCall
	sendErr error
}

type pipelineAgentRPCCall struct {
	AgentID string
	Method  string
	Payload any
}

func (r *recordingPipelineAgentRPC) SendRPC(_ context.Context, agentID, method string, payload any, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, pipelineAgentRPCCall{AgentID: agentID, Method: method, Payload: payload})
	return r.sendErr
}

func (r *recordingPipelineAgentRPC) snapshot() []pipelineAgentRPCCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pipelineAgentRPCCall(nil), r.calls...)
}

func TestAgentBackendDispatchUsesPipelinePlacement(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "agent-1",
		Kind:         iagent.KindK8s,
		Labels:       map[string]string{"label": "gpu"},
		Capabilities: []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Label: "gpu"}}}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	calls := rpc.snapshot()
	if calls[0].AgentID != "agent-1" {
		t.Fatalf("agent id = %q", calls[0].AgentID)
	}
	if calls[0].Method != iagent.MethodPipelineDispatch {
		t.Fatalf("method = %q", calls[0].Method)
	}
}

func TestAgentBackendCancelUsesDispatchAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Kind: iagent.KindK8s, Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	calls := rpc.snapshot()
	if calls[1].Method != iagent.MethodPipelineCancelRun {
		t.Fatalf("method = %q", calls[1].Method)
	}
}

func TestAgentBackendCancelCarriesPipelineNamespace(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Kind: iagent.KindK8s, Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{}, K8s: &manifest.DriverK8sSpec{Namespace: "runs"}}}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	payload := rpc.snapshot()[1].Payload.(map[string]any)
	if payload["namespace"] != "runs" {
		t.Fatalf("namespace = %q, want runs", payload["namespace"])
	}
}

func TestAgentBackendPinsAllRunStepsToOneAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	reg.Register(iagent.Info{ID: "agent-2", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})

	tasks := []*proto.Task{
		{ID: "run-1:first", RunID: "run-1", Pipeline: pipelineJSON},
		{ID: "run-1:second", RunID: "run-1", Pipeline: pipelineJSON},
	}
	for _, task := range tasks {
		if err := backend.Dispatch(context.Background(), task); err != nil {
			t.Fatalf("Dispatch(%s) returned error: %v", task.ID, err)
		}
	}

	calls := rpc.snapshot()
	if len(calls) != 2 {
		t.Fatalf("dispatch calls = %d, want 2", len(calls))
	}
	if calls[0].AgentID != calls[1].AgentID {
		t.Fatalf("run dispatched to multiple agents: %q and %q", calls[0].AgentID, calls[1].AgentID)
	}
}

func TestAgentBackendPinsConcurrentRunStepsToOneAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	reg.Register(iagent.Info{ID: "agent-2", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, step := range []string{"first", "second"} {
		wg.Add(1)
		go func(step string) {
			defer wg.Done()
			errs <- backend.Dispatch(context.Background(), &proto.Task{
				ID:       "run-1:" + step,
				RunID:    "run-1",
				Pipeline: pipelineJSON,
			})
		}(step)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Dispatch returned error: %v", err)
		}
	}

	calls := rpc.snapshot()
	if len(calls) != 2 {
		t.Fatalf("dispatch calls = %d, want 2", len(calls))
	}
	if calls[0].AgentID != calls[1].AgentID {
		t.Fatalf("concurrent run dispatch used multiple agents: %q and %q", calls[0].AgentID, calls[1].AgentID)
	}
}

func TestAgentBackendReleasesUncommittedRunBindingAfterBusy(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "agent-1",
		Runtime:      iagent.RuntimeDocker,
		Capabilities: []string{iagent.CapabilityPipeline},
		Capacity:     1,
	})
	reg.Register(iagent.Info{
		ID:           "agent-2",
		Runtime:      iagent.RuntimeDocker,
		Capabilities: []string{iagent.CapabilityPipeline},
		Capacity:     1,
	})
	rpc := &recordingPipelineAgentRPC{sendErr: &iagent.BusyError{Reason: "actual worker state is full"}}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{ID: "run-1:first", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err == nil {
		t.Fatal("expected busy dispatch error")
	}
	backend.runMu.Lock()
	_, bound := backend.runAgents[task.RunID]
	backend.runMu.Unlock()
	if bound {
		t.Fatal("run remained bound after every initial dispatch was rejected")
	}

	reg.Remove("agent-1")
	rpc.mu.Lock()
	rpc.sendErr = nil
	rpc.mu.Unlock()
	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("retry on alternate worker failed: %v", err)
	}
	if got := rpc.snapshot()[1].AgentID; got != "agent-2" {
		t.Fatalf("retry worker = %q, want agent-2", got)
	}
}

func TestTaskPlacementRejectsMultipleRunnerLabels(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Steps = []pipeline.Step{
		{Name: "cpu", Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Label: "cpu"}}},
		{Name: "gpu", Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Label: "gpu"}}},
	}
	pipelineJSON, _ := json.Marshal(pl)

	_, err := taskPlacement(&proto.Task{RunID: "run-1", Pipeline: pipelineJSON})
	if err == nil {
		t.Fatal("expected incompatible runner labels to be rejected")
	}
}
