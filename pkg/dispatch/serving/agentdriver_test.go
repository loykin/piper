package servingdispatch

import (
	"context"
	"encoding/json"
	"testing"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/serving"
)

type recordingServingAgentRPC struct {
	calls []servingAgentRPCCall
}

type servingAgentRPCCall struct {
	AgentID string
	Method  string
	Payload any
}

func (r *recordingServingAgentRPC) SendRPC(_ context.Context, agentID, method string, payload any, result any) error {
	r.calls = append(r.calls, servingAgentRPCCall{AgentID: agentID, Method: method, Payload: payload})
	if method == iagent.MethodServingDeploy && result != nil {
		data, _ := json.Marshal(map[string]string{"endpoint": "http://demo.default.svc.cluster.local:8000"})
		_ = json.Unmarshal(data, result)
	}
	return nil
}

func newServingAgentDriver(repo serving.Repository) (*AgentDriver, *recordingServingAgentRPC) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "agent-1",
		Kind:         iagent.KindK8s,
		ClusterName:  "gpu-a",
		Capabilities: []string{iagent.CapabilityServing},
	})
	rpc := &recordingServingAgentRPC{}
	return NewAgentDriver(iagent.NewRouter(reg), rpc, repo), rpc
}

func TestServingAgentDriverDeploy(t *testing.T) {
	driver, rpc := newServingAgentDriver(newStubServingRepo())
	spec := serving.ModelService{}
	spec.Metadata.Name = "demo"
	spec.Spec.Model.FromURI = "s3://models/demo"
	spec.Spec.Runtime.Worker = "agent-1"

	svc, err := driver.Deploy(context.Background(), spec, artifact.Resolved{S3URI: "s3://models/demo"}, "metadata:\n  name: demo\n")
	if err != nil {
		t.Fatalf("Deploy returned error: %v", err)
	}
	if svc.WorkerID != "agent-1" {
		t.Fatalf("worker id = %q", svc.WorkerID)
	}
	if svc.Endpoint != "http://demo.default.svc.cluster.local:8000" {
		t.Fatalf("endpoint = %q", svc.Endpoint)
	}
	if rpc.calls[0].Method != iagent.MethodServingDeploy {
		t.Fatalf("method = %q", rpc.calls[0].Method)
	}
}

func TestServingAgentDriverDeployNoAgent(t *testing.T) {
	reg := iagent.NewRegistry() // empty registry → no serving agent available
	rpc := &recordingServingAgentRPC{}
	driver := NewAgentDriver(iagent.NewRouter(reg), rpc, newStubServingRepo())
	spec := serving.ModelService{}
	spec.Metadata.Name = "demo"

	if _, err := driver.Deploy(context.Background(), spec, artifact.Resolved{S3URI: "s3://models/demo"}, ""); err == nil {
		t.Fatal("expected error when no serving agent is registered")
	}
}

func TestServingAgentDriverStopAndRestart(t *testing.T) {
	repo := newStubServingRepo(&serving.Service{Name: "demo", WorkerID: "agent-1"})
	driver, rpc := newServingAgentDriver(repo)
	spec := serving.ModelService{}
	spec.Metadata.Name = "demo"
	spec.Spec.Model.FromURI = "s3://models/demo"

	if err := driver.Stop(context.Background(), &serving.Service{Name: "demo", WorkerID: "agent-1"}); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if err := driver.Restart(context.Background(), spec, artifact.Resolved{S3URI: "s3://models/demo"}, "metadata:\n  name: demo\n"); err != nil {
		t.Fatalf("Restart returned error: %v", err)
	}
	if rpc.calls[0].Method != iagent.MethodServingStop {
		t.Fatalf("first method = %q", rpc.calls[0].Method)
	}
	if rpc.calls[1].Method != iagent.MethodServingRestart {
		t.Fatalf("second method = %q", rpc.calls[1].Method)
	}
}
