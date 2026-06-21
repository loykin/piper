package servingdispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/artifact"
	"github.com/piper/piper/pkg/serving"
)

// stubServingPolicyRepo is a minimal WorkerPodPolicyRepository for serving tests.
type stubServingPolicyRepo struct {
	policies map[string]*iagent.WorkerPodPolicy
}

func newStubServingPolicyRepo(entries ...*iagent.WorkerPodPolicy) *stubServingPolicyRepo {
	r := &stubServingPolicyRepo{policies: make(map[string]*iagent.WorkerPodPolicy)}
	for _, e := range entries {
		r.policies[e.WorkerID] = e
	}
	return r
}

func (r *stubServingPolicyRepo) List(_ context.Context) ([]iagent.WorkerPodPolicy, error) {
	return nil, nil
}

func (r *stubServingPolicyRepo) Get(_ context.Context, workerID string) (*iagent.WorkerPodPolicy, error) {
	return r.policies[workerID], nil
}

func (r *stubServingPolicyRepo) Set(_ context.Context, p iagent.WorkerPodPolicy) error {
	r.policies[p.WorkerID] = &p
	return nil
}

func (r *stubServingPolicyRepo) Delete(_ context.Context, workerID string) error {
	delete(r.policies, workerID)
	return nil
}

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

func TestServingAgentDriverDeploy_AppliesPodPolicy(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "agent-1",
		Infrastructure: iagent.InfrastructureK8s,
		ClusterName:    "gpu-a",
		Capabilities:   []string{iagent.CapabilityServing},
	})
	rpc := &recordingServingAgentRPC{}
	repo := newStubServingPolicyRepo(&iagent.WorkerPodPolicy{
		WorkerID: "agent-1",
		PodTemplate: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"nvidia.com/gpu": "true"},
			},
		},
	})
	driver := NewAgentDriver(iagent.NewRouter(reg), rpc, newStubServingRepo(), repo)

	spec := serving.ModelService{}
	spec.Metadata.Name = "gpu-model"
	spec.Spec.Driver.Placement.Worker = "agent-1"

	const modelYAML = `apiVersion: piper.dev/v1
kind: ModelService
metadata:
  name: gpu-model
spec:
  driver:
    placement:
      runtime: k8s
    k8s:
      image: mymodel:latest
      namespace: ml
  run:
    command: ["python", "serve.py"]
`
	_, err := driver.Deploy(context.Background(), spec, artifact.Resolved{S3URI: "s3://models/gpu-model"}, modelYAML)
	if err != nil {
		t.Fatalf("Deploy returned error: %v", err)
	}
	if len(rpc.calls) == 0 {
		t.Fatal("expected an RPC call")
	}
	payload, ok := rpc.calls[0].Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type: %T", rpc.calls[0].Payload)
	}
	sentYAML, _ := payload["yaml"].(string)
	if !strings.Contains(sentYAML, "nvidia.com/gpu") {
		t.Errorf("pod policy nodeSelector not found in sent YAML:\n%s", sentYAML)
	}
}

func TestServingAgentDriverDeploy_ManifestWinsOverPolicy(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "agent-1",
		Capabilities: []string{iagent.CapabilityServing},
	})
	rpc := &recordingServingAgentRPC{}
	repo := newStubServingPolicyRepo(&iagent.WorkerPodPolicy{
		WorkerID: "agent-1",
		PodTemplate: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"tier": "policy"},
			},
		},
	})
	driver := NewAgentDriver(iagent.NewRouter(reg), rpc, newStubServingRepo(), repo)

	spec := serving.ModelService{}
	spec.Metadata.Name = "custom"
	spec.Spec.Driver.Placement.Worker = "agent-1"

	const modelYAML = `apiVersion: piper.dev/v1
kind: ModelService
metadata:
  name: custom
spec:
  driver:
    placement:
      runtime: k8s
    k8s:
      image: mymodel:latest
      namespace: ml
      pod_template:
        spec:
          nodeSelector:
            tier: manifest
  run:
    command: ["python", "serve.py"]
`
	_, err := driver.Deploy(context.Background(), spec, artifact.Resolved{S3URI: "s3://models/custom"}, modelYAML)
	if err != nil {
		t.Fatalf("Deploy returned error: %v", err)
	}
	payload := rpc.calls[0].Payload.(map[string]any)
	sentYAML, _ := payload["yaml"].(string)
	if !strings.Contains(sentYAML, "manifest") {
		t.Errorf("manifest nodeSelector should win over policy; YAML:\n%s", sentYAML)
	}
}
