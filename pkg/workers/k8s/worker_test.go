package k8sworker

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/tunnel"
)

func TestBuildTunnelURL(t *testing.T) {
	got, err := BuildTunnelURL("https://piper.example.com/base/", "agent 1")
	if err != nil {
		t.Fatalf("BuildTunnelURL returned error: %v", err)
	}
	want := "wss://piper.example.com/base/api/agents/agent%201/tunnel"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestWorkloadHandlersAreRegisteredWhenK8sClientExists(t *testing.T) {
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a", K8sClient: fake.NewSimpleClientset()})
	payload, _ := json.Marshal(map[string]string{"volume_id": "vol-123"})

	resp := a.client.Dispatcher().Handle(context.Background(), tunnel.Frame{
		Type:    tunnel.FrameRPCRequest,
		ID:      "1",
		Method:  iagent.MethodNotebookProvisionVolume,
		Payload: payload,
	})
	if resp.Status != "ok" {
		t.Fatalf("status = %q error=%q", resp.Status, resp.Error)
	}
}

func TestWorkloadHandlersAreNotRegisteredWithoutK8sClient(t *testing.T) {
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a"})

	resp := a.client.Dispatcher().Handle(context.Background(), tunnel.Frame{
		Type:   tunnel.FrameRPCRequest,
		ID:     "1",
		Method: iagent.MethodNotebookProvisionVolume,
	})
	if resp.Status != "error" {
		t.Fatalf("status = %q, want error", resp.Status)
	}
}
