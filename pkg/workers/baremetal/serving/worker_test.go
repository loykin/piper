package servingworker

import (
	"context"
	"testing"
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
