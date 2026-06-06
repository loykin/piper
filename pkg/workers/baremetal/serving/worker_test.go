package servingworker

import (
	"context"
	"testing"

	"github.com/piper/piper/pkg/serving"
)

func TestServingWorker_DeployEmptyCommand(t *testing.T) {
	w := New(Config{ID: "test-id"})
	yamlPayload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: no-cmd-svc
spec:
  runtime:
    port: 8080
`
	_, err := w.deploy(context.Background(), deployRequest{YAML: yamlPayload})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestServingWorker_DeployNoPort(t *testing.T) {
	w := New(Config{ID: "test-id"})
	yamlPayload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: no-port-svc
spec:
  runtime:
    command: ["echo"]
`
	_, err := w.deploy(context.Background(), deployRequest{YAML: yamlPayload})
	if err == nil {
		t.Fatal("expected error for missing port")
	}
}

func TestServingWorker_DeployInvalidYAML(t *testing.T) {
	w := New(Config{ID: "test-id"})
	_, err := w.deploy(context.Background(), deployRequest{YAML: ":::not yaml:::"})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestServingWorker_StopNonExistent(t *testing.T) {
	w := New(Config{ID: "test-id"})
	// stop is idempotent: stopping a non-existent service is not an error.
	if err := w.stop(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("stop nonexistent: %v", err)
	}
}

func TestServingWorker_StaleGenerationCannotMutateCurrentService(t *testing.T) {
	w := New(Config{ID: "test-id"})

	oldGen, err := w.reserveService("demo", "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	w.removeService("demo", oldGen)
	newGen, err := w.reserveService("demo", "http://localhost:8081")
	if err != nil {
		t.Fatal(err)
	}

	if w.updateService("demo", oldGen, serving.StatusFailed, "") {
		t.Fatal("stale generation updated current service")
	}
	if w.removeService("demo", oldGen) {
		t.Fatal("stale generation removed current service")
	}
	if !w.updateService("demo", newGen, serving.StatusRunning, "http://localhost:8081") {
		t.Fatal("current generation was not updated")
	}

	w.mu.Lock()
	rec := w.services["demo"]
	w.mu.Unlock()
	if rec == nil || rec.status != serving.StatusRunning || rec.endpoint != "http://localhost:8081" {
		t.Fatalf("current service = %#v", rec)
	}
}

func TestServingWorker_HealthFailureOverridesStopStatus(t *testing.T) {
	w := New(Config{ID: "test-id"})
	gen, err := w.reserveService("demo", "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}

	if !w.failService("demo", gen) {
		t.Fatal("failed to mark service failed")
	}
	removed, status := w.completeService("demo", gen, serving.StatusStopped)
	if !removed {
		t.Fatal("service was not completed")
	}
	if status != serving.StatusFailed {
		t.Fatalf("status = %q, want failed", status)
	}
}

func TestServingWorker_RejectsDuplicateActiveService(t *testing.T) {
	w := New(Config{ID: "test-id"})
	if _, err := w.reserveService("demo", "http://localhost:8080"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.reserveService("demo", "http://localhost:8081"); err == nil {
		t.Fatal("expected duplicate active service to be rejected")
	}
}
