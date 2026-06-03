package k8sworker

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
)

// nopHandler is a do-nothing handler used to probe whether a method is registered.
func nopHandler(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil }

// isRegistered returns true if the dispatcher already has a handler for method
// (detected by attempting to register a duplicate, which returns an error).
func isRegistered(d *grpcagent.Dispatcher, method string) bool {
	err := d.Register(method, nopHandler)
	return err != nil // "already registered" error means the handler exists
}

func TestWorkloadHandlersAreRegisteredWhenK8sClientExists(t *testing.T) {
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a", K8sClient: fake.NewSimpleClientset()})
	d := a.client.Dispatcher()

	methods := []string{
		iagent.MethodNotebookProvisionVolume,
		iagent.MethodNotebookStart,
		iagent.MethodNotebookStop,
		iagent.MethodServingDeploy,
		iagent.MethodServingStop,
	}
	for _, m := range methods {
		if !isRegistered(d, m) {
			t.Errorf("handler for %q should be registered when K8sClient is set", m)
		}
	}
}

func TestWorkloadHandlersAreNotRegisteredWithoutK8sClient(t *testing.T) {
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a"})
	d := a.client.Dispatcher()

	methods := []string{
		iagent.MethodNotebookProvisionVolume,
		iagent.MethodNotebookStart,
		iagent.MethodNotebookStop,
		iagent.MethodServingDeploy,
		iagent.MethodServingStop,
	}
	for _, m := range methods {
		if isRegistered(d, m) {
			t.Errorf("handler for %q should NOT be registered without K8sClient", m)
		}
	}
}
