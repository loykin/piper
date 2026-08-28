package piper

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/runlifecycle"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/integration/mlflow"
	"github.com/loykin/piper/pkg/integration/outbox"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/project"
)

// setUpMlflowIntegration creates a project-scoped mlflow credential and an
// enabled, ExportPipelines, Default MLflowIntegration pointing at
// trackingURI — the minimal fixture StartRun's real EnqueuePipelineCreated
// wiring (piper.go) needs to actually enqueue an outbox event.
func setUpMlflowIntegration(t *testing.T, p *Piper, projectID, trackingURI string) *mlflow.MLflowIntegration {
	t.Helper()
	ctx := context.Background()
	if _, err := p.credentials.Create(ctx, projectID, credential.CreateRequest{
		Name: "mlflow-cred", Kind: credential.KindMlflow, Data: map[string]string{"token": "test-token"},
	}); err != nil {
		t.Fatalf("create mlflow credential: %v", err)
	}
	integration := &mlflow.MLflowIntegration{
		ID: "int-1", ProjectID: projectID, Name: "default",
		TrackingURI: trackingURI, CredentialRef: "mlflow-cred",
		Enabled: true, Default: true, ExportPipelines: true,
		ExperimentTemplate: mlflow.DefaultExperimentTemplate, ArtifactMode: string(mlflow.ArtifactModeReference),
	}
	if err := p.repos.Mlflow.CreateIntegration(ctx, integration); err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	return integration
}

func trivialPipeline(name string) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: name, Version: 3},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{{
			Name: "step",
			Run:  pipeline.Run{Command: []string{"true"}},
		}}},
	}
}

// TestStartRun_EnqueuesMlflowExportEventsWhenIntegrationConfigured verifies
// the actual wiring this task's brief describes: internal/runlifecycle's
// StartRun (via Deps.EnqueuePipelineCreated, set in piper.go's New) durably
// records a pipeline_run.created outbox event when the project has an
// enabled default MLflow integration, and queue.go's OnRunOutcome (also
// wired in piper.go) records pipeline_run.finished once the run reaches a
// terminal status.
func TestStartRun_EnqueuesMlflowExportEventsWhenIntegrationConfigured(t *testing.T) {
	// RequestTimeout kept short and Enabled left true (the default) so the
	// background dispatcher's inevitable failed delivery attempts (this
	// TrackingURI never resolves — RFC 2606 .invalid) fail fast instead of
	// dragging out the test; the test only asserts on the outbox row
	// itself, never on successful delivery.
	cfg := Config{OutputDir: t.TempDir()}
	cfg.Integrations.Mlflow.RequestTimeout = 2 * time.Second
	p := newTestPiper(t, cfg)
	const projectID = "mlflow-enqueue-project"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	integration := setUpMlflowIntegration(t, p, projectID, "https://piper-mlflow-test-does-not-exist.invalid")

	pl := trivialPipeline("train")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := p.runs.StartRun(context.Background(), pl, dag, runlifecycle.StartRunOptions{
		ProjectID: projectID,
		YAML:      "metadata:\n  name: train\n  version: 3\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitRunTerminal(t, p, projectID, runID, 5*time.Second)

	got, err := p.repos.Run.Get(context.Background(), projectID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" {
		t.Fatalf("run status = %q, want success (MLflow being unreachable must never fail the run)", got.Status)
	}

	// pipeline_run.finished's OnRunOutcome hook fires asynchronously right
	// after the terminal-status CAS commits (queue.go's appendEffect) — give
	// it a brief moment rather than asserting immediately.
	var events []*outbox.Event
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err = p.repos.Outbox.ListByAggregate(context.Background(), integration.ID, outbox.AggregateTypePipelineRun, runID)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(events) < 2 {
		t.Fatalf("outbox events for run %q = %d, want at least 2 (created + finished); got %+v", runID, len(events), events)
	}
	var sawCreated, sawFinished bool
	for _, e := range events {
		switch e.EventType {
		case mlflow.EventTypePipelineRunCreated:
			sawCreated = true
		case mlflow.EventTypePipelineRunFinished:
			sawFinished = true
		}
	}
	if !sawCreated || !sawFinished {
		t.Fatalf("expected both created and finished events, got %+v", events)
	}
	if events[0].Sequence >= events[1].Sequence {
		t.Fatalf("events not ordered by ascending sequence: %+v", events)
	}
}

// TestStartRun_NoMlflowIntegrationConfiguredEnqueuesNothing is the
// complementary case: a project with no MLflow integration at all must not
// accumulate any outbox backlog — EnqueuePipelineCreated's
// resolveExportIntegration gate is a no-op, not an error.
func TestStartRun_NoMlflowIntegrationConfiguredEnqueuesNothing(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	const projectID = "mlflow-noop-project"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	pl := trivialPipeline("train")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := p.runs.StartRun(context.Background(), pl, dag, runlifecycle.StartRunOptions{
		ProjectID: projectID,
		YAML:      "metadata:\n  name: train\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitRunTerminal(t, p, projectID, runID, 5*time.Second)

	pending, err := p.repos.Outbox.CountByStatus(context.Background(), "int-1", string(outbox.StatusPending))
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("pending outbox count = %d, want 0 (no integration configured for this project)", pending)
	}
}
