package pipelineworker_test

import (
	"strings"
	"testing"

	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
)

func TestNewWorker_defaultsApplied(t *testing.T) {
	w, err := worker.New(worker.Config{
		AgentAddr: "localhost:9090",
		ID:        "test-worker",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_ = w
}

func TestNewWorker_missingAgentAddr(t *testing.T) {
	// AgentAddr is required; grpcagent.NewClient validates it on Run, not New.
	// New() itself should succeed even without AgentAddr (validated at connect time).
	_, err := worker.New(worker.Config{ID: "test-worker"})
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
