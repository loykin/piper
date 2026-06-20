package pipelineworker_test

import (
	"strings"
	"testing"

	worker "github.com/piper/piper/pkg/pipeline/worker"
)

func TestNewWorker_defaultsApplied(t *testing.T) {
	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{MasterURL: "http://localhost:8080", ID: "test-worker"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_ = w
}

func TestNewWorker_missingMasterURL(t *testing.T) {
	// MasterURL is validated when the tunnel connects, not by New.
	_, err := worker.New(worker.Config{Agent: worker.AgentConfig{ID: "test-worker"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewID_format(t *testing.T) {
	id := worker.NewID("myworker")
	if id == "" {
		t.Fatal("NewID returned empty string")
	}
	// Should be lowercase with dashes, no other characters.
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			t.Fatalf("NewID returned invalid character %q in %q", c, id)
		}
	}
	if strings.HasPrefix(id, "-") || strings.HasSuffix(id, "-") {
		t.Fatalf("NewID should not start or end with dash: %q", id)
	}
}
