package piper

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/project"
)

const startRunCutoverYAML = "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: cutover-pipeline\nspec:\n  steps:\n  - name: only\n    run:\n      type: command\n      command: [\"true\"]\n"

// TestStartRunUsesDispatchRunWhenBackendSupportsIt confirms the cutover:
// Piper.startRun must call RunDispatchBackend.DispatchRun instead of the
// legacy per-step queue.AddWithEnv path whenever the configured backend
// supports it (which — see internal/pipelinedispatch/backend.go's
// RunDispatchBackend doc comment — is every AgentBackend instance in this
// repo; the fallback only exists for a caller-supplied ExecutionBackend via
// the public Piper.SetBackend API).
func TestStartRunUsesDispatchRunWhenBackendSupportsIt(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	projectID := project.DefaultID
	ctx := project.WithContext(context.Background(), project.Context{ID: projectID})

	backend := &fakeRunDispatchBackend{}
	p.SetBackend(backend)

	runID, err := p.StartRun(ctx, startRunCutoverYAML, nil, BuiltinVars{})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(backend.calls()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	calls := backend.calls()
	if len(calls) != 1 {
		t.Fatalf("DispatchRun called %d times, want 1: %#v", len(calls), calls)
	}
	if calls[0].RunID != runID {
		t.Fatalf("dispatched RunID = %q, want %q", calls[0].RunID, runID)
	}
	if calls[0].ProjectID != projectID {
		t.Fatalf("dispatched ProjectID = %q, want %q", calls[0].ProjectID, projectID)
	}
	if calls[0].PipelineYAML == "" {
		t.Fatal("dispatched PipelineYAML is empty")
	}
}
