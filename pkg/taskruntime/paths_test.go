package taskruntime

import (
	"strings"
	"testing"
)

func TestRuntimeKeyIsStableAndRuntimeSafe(t *testing.T) {
	got := RuntimeKey("Worker 1", "run/1", "Train Model", 2)
	if got != "worker-1-run-1-train-model-a2" {
		t.Fatalf("runtime key = %q", got)
	}
}

func TestRuntimeKeyTruncationPreservesUniqueness(t *testing.T) {
	prefix := strings.Repeat("long-name-", 10)
	first := RuntimeKey("worker", prefix, "step", 1)
	second := RuntimeKey("worker", prefix, "step", 2)
	if len(first) > 63 || len(second) > 63 {
		t.Fatalf("runtime keys exceed 63 characters: %d, %d", len(first), len(second))
	}
	if first == second {
		t.Fatalf("attempt-specific runtime keys collided: %q", first)
	}
}
