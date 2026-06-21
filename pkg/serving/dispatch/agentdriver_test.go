package servingdispatch

import (
	"context"
	"encoding/json"
	"testing"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/artifact"
	"github.com/piper/piper/pkg/serving"
)

// stubServingRepo is a minimal in-memory serving.Repository for tests.
type stubServingRepo struct {
	services map[string]*serving.Service
}

func newStubServingRepo(initial ...*serving.Service) *stubServingRepo {
	r := &stubServingRepo{services: make(map[string]*serving.Service)}
	for _, s := range initial {
		r.services[s.Name] = s
	}
	return r
}

func (r *stubServingRepo) Get(_ context.Context, _, name string) (*serving.Service, error) {
	s, ok := r.services[name]
	if !ok {
		return nil, nil
	}
	return s, nil
}

func (r *stubServingRepo) Create(_ context.Context, s *serving.Service) error {
	r.services[s.Name] = s
	return nil
}

func (r *stubServingRepo) Update(_ context.Context, s *serving.Service) error {
	r.services[s.Name] = s
	return nil
}

func (r *stubServingRepo) List(_ context.Context, _ string) ([]*serving.Service, error) {
	out := make([]*serving.Service, 0, len(r.services))
	for _, s := range r.services {
		out = append(out, s)
	}
	return out, nil
}

func (r *stubServingRepo) ListByWorker(_ context.Context, _ string) ([]*serving.Service, error) {
	return r.List(context.Background(), "")
}
func (r *stubServingRepo) Delete(_ context.Context, _, name string) error {
	delete(r.services, name)
	return nil
}

func (r *stubServingRepo) SetStatus(_ context.Context, _, name, status string) error {
	if s, ok := r.services[name]; ok {
		s.Status = status
	}
	return nil
}

func (r *stubServingRepo) ListHistory(_ context.Context, _ string) ([]*serving.ServiceHistory, error) {
	return nil, nil
}

func (r *stubServingRepo) Upsert(_ context.Context, s *serving.Service) error {
	r.services[s.Name] = s
	return nil
}

func (r *stubServingRepo) SetStatusEndpoint(_ context.Context, _, name, status, endpoint string) error {
	if s, ok := r.services[name]; ok {
		s.Status = status
		s.Endpoint = endpoint
	}
	return nil
}

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
		ID:             "agent-1",
		Infrastructure: iagent.InfrastructureK8s,
		ClusterName:    "gpu-a",
		Capabilities:   []string{iagent.CapabilityServing},
	})
	rpc := &recordingServingAgentRPC{}
	return NewAgentDriver(iagent.NewRouter(reg), rpc, repo), rpc
}

func TestServingAgentDriverDeploy(t *testing.T) {
	driver, rpc := newServingAgentDriver(newStubServingRepo())
	spec := serving.ModelService{}
	spec.Metadata.Name = "demo"
	spec.Spec.Model.FromURI = "s3://models/demo"
	spec.Spec.Driver.Placement.Worker = "agent-1"

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
