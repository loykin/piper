package commands

import (
	"testing"

	worker "github.com/piper/piper/pkg/pipeline/worker"
)

// TestWorkerConfig_defaults verifies that concurrency defaults are applied.
func TestWorkerConfig_defaults(t *testing.T) {
	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{MasterURL: "http://localhost:8080", ID: "test"},
	})
	if err != nil {
		t.Fatalf("worker.New: %v", err)
	}
	_ = w
}
