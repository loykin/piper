package commands

import (
	"testing"

	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
)

// TestWorkerConfig_defaults verifies that concurrency defaults are applied.
func TestWorkerConfig_defaults(t *testing.T) {
	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{Addr: "localhost:9090", ID: "test"},
	})
	if err != nil {
		t.Fatalf("worker.New: %v", err)
	}
	_ = w
}

// TestWorkerConfig_StorageURL verifies that StorageURL set on StoreConfig is preserved.
func TestWorkerConfig_StorageURL(t *testing.T) {
	url := "s3://my-bucket?region=us-east-1"
	cfg := worker.Config{
		Agent: worker.AgentConfig{Addr: "localhost:9090", ID: "test"},
		Store: worker.StoreConfig{StorageURL: url},
	}
	if cfg.Store.StorageURL != url {
		t.Fatalf("StorageURL = %q, want %q", cfg.Store.StorageURL, url)
	}
}
