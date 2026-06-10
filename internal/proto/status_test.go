package proto_test

import (
	"testing"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline/run"
)

// TestStatusConsistency verifies that status string literals are aligned across
// the proto, run, and queue layers. Mismatches cause silent filtering bugs
// (e.g. the frontend filtering by "success" while the DB stores "done").
func TestStatusConsistency(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		// Worker/agent report "done" on success; queue maps this to run.StatusSuccess
		// when finalising a run. Step-level status stays "done"; run-level becomes "success".
		{"TaskStatusDone value", proto.TaskStatusDone, "done"},
		{"TaskStatusFailed value", proto.TaskStatusFailed, "failed"},

		// Run-level status values used by the DB and API.
		{"run.StatusRunning", run.StatusRunning, "running"},
		{"run.StatusSuccess", run.StatusSuccess, "success"},
		{"run.StatusFailed", run.StatusFailed, "failed"},

		// Steps are "done" (not "success") — this deliberate difference is documented here.
		// The frontend displays step status as-is; run status uses "success".
		{"step done != run success", proto.TaskStatusDone, "done"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}
