package piper

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

const rerunDispatchTwoStepYAML = "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: rerun-pipeline\nspec:\n  steps:\n  - name: good\n    run:\n      type: command\n      command: [\"true\"]\n  - name: bad\n    depends_on: [good]\n    run:\n      type: command\n      command: [\"false\"]\n"

// TestRerunRunFailedOnlyDispatchesOnlyFilteredSteps is a regression test for
// a bug the startRun cutover introduced: rerunRun(failedOnly=true) filters
// pl.Spec.Steps down to just the failed subset before calling startRun, but
// startRun's dispatch path originally used opts.YAML (the ORIGINAL,
// unfiltered manifest, kept only for the run row's audit trail) instead of
// marshaling the filtered pl — which would have sent the worker the full
// original pipeline and re-run every step instead of just the failed one.
func TestRerunRunFailedOnlyDispatchesOnlyFilteredSteps(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	projectID := project.DefaultID
	ctx := project.WithContext(context.Background(), project.Context{ID: projectID})

	backend := &fakeRunDispatchBackend{}
	p.SetBackend(backend)

	const prevRunID = "run-rerun-source"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           prevRunID,
		PipelineName: "rerun-pipeline",
		Status:       run.StatusFailed,
		StartedAt:    time.Now().UTC(),
		PipelineYAML: rerunDispatchTwoStepYAML,
		ParamsJSON:   "{}",
	}); err != nil {
		t.Fatalf("create previous run: %v", err)
	}
	if err := p.repos.Step.Upsert(ctx, &run.Step{ProjectID: projectID, RunID: prevRunID, StepName: "good", Status: "done"}); err != nil {
		t.Fatalf("upsert step good: %v", err)
	}
	if err := p.repos.Step.Upsert(ctx, &run.Step{ProjectID: projectID, RunID: prevRunID, StepName: "bad", Status: "failed"}); err != nil {
		t.Fatalf("upsert step bad: %v", err)
	}

	newRunID, err := p.rerunRun(ctx, prevRunID, true)
	if err != nil {
		t.Fatalf("rerunRun: %v", err)
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
	if calls[0].RunID != newRunID {
		t.Fatalf("dispatched RunID = %q, want %q", calls[0].RunID, newRunID)
	}

	dispatchedPl, err := pipeline.Parse([]byte(calls[0].PipelineYAML))
	if err != nil {
		t.Fatalf("parse dispatched pipeline yaml: %v\nyaml:\n%s", err, calls[0].PipelineYAML)
	}
	if len(dispatchedPl.Spec.Steps) != 1 || dispatchedPl.Spec.Steps[0].Name != "bad" {
		t.Fatalf("dispatched steps = %#v, want exactly [bad] (the failed step only, not the full original pipeline)", dispatchedPl.Spec.Steps)
	}
}
