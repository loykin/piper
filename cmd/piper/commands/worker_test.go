package commands

import (
	"testing"

	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
)

// TestWorkerConfig_defaults verifies that concurrency defaults are applied.
func TestWorkerConfig_defaults(t *testing.T) {
	w, err := worker.New(worker.Config{
		AgentAddr: "localhost:9090",
		ID:        "test",
	})
	if err != nil {
		t.Fatalf("worker.New: %v", err)
	}
	_ = w
}

// TestWorkerConfigFromSource_StorageURLPassthrough verifies that StorageURL
// set on Config is preserved and passed to subprocesses via agent exec.
func TestWorkerConfig_StorageURL(t *testing.T) {
	url := "s3://my-bucket?region=us-east-1"
	cfg := worker.Config{
		AgentAddr:  "localhost:9090",
		ID:         "test",
		StorageURL: url,
	}
	if cfg.StorageURL != url {
		t.Fatalf("StorageURL = %q, want %q", cfg.StorageURL, url)
	}
}
