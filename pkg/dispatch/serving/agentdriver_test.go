package servingdispatch

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if method == iagent.MethodServingDeploy {
		res := result.(*struct {
			Endpoint  string `json:"endpoint"`
			Namespace string `json:"namespace"`
		})
		res.Endpoint = "http://demo.default.svc.cluster.local:8000"
		res.Namespace = "default"
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

func TestServingAgentDriverRequiresS3Artifact(t *testing.T) {
	driver, _ := newServingAgentDriver(newStubServingRepo())
	spec := serving.ModelService{}
	spec.Metadata.Name = "demo"

	if _, err := driver.Deploy(context.Background(), spec, artifact.Resolved{LocalPath: "/tmp/model"}, ""); err == nil {
		t.Fatal("expected S3 artifact error")
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

func TestServingAgentDriverBareMetalUsesDirectWorkerHTTP(t *testing.T) {
	var sawDeploy bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/deploy" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		sawDeploy = true
		_, _ = w.Write([]byte(`{"endpoint":"http://localhost:9000"}`))
	}))
	defer worker.Close()

	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "worker-1",
		Kind:         iagent.KindBareMetal,
		Addr:         worker.URL,
		Capabilities: []string{iagent.CapabilityServing},
	})
	driver := NewAgentDriver(iagent.NewRouter(reg), &recordingServingAgentRPC{}, newStubServingRepo(), "http://master")
	spec := serving.ModelService{}
	spec.Metadata.Name = "demo"
	spec.Spec.Model.FromURI = "file:///tmp/model"

	svc, err := driver.Deploy(context.Background(), spec, artifact.Resolved{LocalPath: "/tmp/model"}, "metadata:\n  name: demo\n")
	if err != nil {
		t.Fatalf("Deploy returned error: %v", err)
	}
	if !sawDeploy {
		t.Fatal("worker deploy endpoint was not called")
	}
	if svc.WorkerID != "worker-1" {
		t.Fatalf("worker id = %q", svc.WorkerID)
	}
	if svc.Endpoint != "http://localhost:9000" {
		t.Fatalf("endpoint = %q", svc.Endpoint)
	}
}
