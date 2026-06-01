package backend

import (
	"context"
	"encoding/json"
	"testing"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

type recordingPipelineAgentRPC struct {
	calls []pipelineAgentRPCCall
}

type pipelineAgentRPCCall struct {
	AgentID string
	Method  string
	Payload any
}

func (r *recordingPipelineAgentRPC) SendRPC(_ context.Context, agentID, method string, payload any, _ any) error {
	r.calls = append(r.calls, pipelineAgentRPCCall{AgentID: agentID, Method: method, Payload: payload})
	return nil
}

func TestAgentBackendDispatchUsesPipelinePlacement(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "agent-1",
		Kind:         iagent.KindK8s,
		ClusterName:  "gpu-a",
		Capabilities: []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pl := pipeline.Pipeline{}
	pl.Spec.Placement.Cluster = "gpu-a"
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if rpc.calls[0].AgentID != "agent-1" {
		t.Fatalf("agent id = %q", rpc.calls[0].AgentID)
	}
	if rpc.calls[0].Method != iagent.MethodPipelineDispatch {
		t.Fatalf("method = %q", rpc.calls[0].Method)
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
	if rpc.calls[1].Method != iagent.MethodPipelineCancelRun {
		t.Fatalf("method = %q", rpc.calls[1].Method)
	}
}

func TestAgentBackendCancelCarriesPipelineNamespace(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Kind: iagent.KindK8s, Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc)
	pl := pipeline.Pipeline{}
	pl.Spec.Placement.Namespace = "runs"
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	payload := rpc.calls[1].Payload.(map[string]any)
	if payload["namespace"] != "runs" {
		t.Fatalf("namespace = %q, want runs", payload["namespace"])
	}
}
