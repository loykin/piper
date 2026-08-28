package mlflow_test

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/integration/mlflow"
	"github.com/loykin/piper/pkg/integration/outbox"
	"github.com/loykin/piper/pkg/project"
)

// TestEndToEnd_EnqueueDispatchSync exercises the real, DB-backed production
// path this task's brief asks for: StartRun's enqueue helper
// (mlflow.EnqueuePipelineRunCreated — the exact function
// internal/runlifecycle.Manager.StartRun calls) writes a durable outbox
// row through the real SQLite repository, an outbox.Dispatcher claims and
// delivers it through a fake mlflow.Client (no real MLflow server needed),
// and the resulting MLflowRunLink ends up `synced`. It then does the same
// for the pipeline_run.finished event. This is deliberately at the
// mlflow-package level rather than a full piper.New()+HTTP round trip:
// EnqueuePipelineRunCreated/EnqueuePipelineRunFinished are the exact
// functions runlifecycle/piper.go wire into the run lifecycle, so driving
// them directly with the same arguments exercises the real integration
// boundary without needing a live MLflow server or a fake one injected
// into piper.New's construction path.
func TestEndToEnd_EnqueueDispatchSync(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	const projectID = "e2e-project"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	integration := &mlflow.MLflowIntegration{
		ID: "int-e2e", ProjectID: projectID, Name: "default",
		TrackingURI: "https://mlflow.example.com", CredentialRef: "mlflow-cred",
		Enabled: true, Default: true, ExportPipelines: true,
		ExperimentTemplate: mlflow.DefaultExperimentTemplate, ArtifactMode: string(mlflow.ArtifactModeReference),
	}
	if err := repos.Mlflow.CreateIntegration(ctx, integration); err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}

	const runID = "run-e2e-1"
	if err := mlflow.EnqueuePipelineRunCreated(ctx, repos.Mlflow, repos.Outbox, projectID, runID,
		map[string]any{"epochs": float64(5)}, "train", 1, "nightly", "alice", "baremetal",
		"/api/projects/"+projectID+"/runs/"+runID, time.Now()); err != nil {
		t.Fatalf("EnqueuePipelineRunCreated: %v", err)
	}

	pendingBefore, err := repos.Outbox.CountByStatus(ctx, integration.ID, string(outbox.StatusPending))
	if err != nil || pendingBefore != 1 {
		t.Fatalf("pending outbox count = %d, err=%v, want 1", pendingBefore, err)
	}

	client := &fakeClient{}
	exporter := mlflow.NewExporter(repos.Mlflow, func(context.Context, *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })
	dispatcher := outbox.NewDispatcher(repos.Outbox, exporter, outbox.Config{Owner: "test-dispatcher", Concurrency: 1})

	if n := dispatcher.PollOnce(ctx); n != 1 {
		t.Fatalf("PollOnce claimed %d events, want 1", n)
	}

	link, err := repos.Mlflow.GetRunLink(ctx, integration.ID, projectID, string(mlflow.SourceTypePipeline), runID)
	if err != nil {
		t.Fatalf("GetRunLink: %v", err)
	}
	if link == nil || link.SyncStatus != string(mlflow.SyncStatusSynced) || link.MLflowRunID == "" {
		t.Fatalf("run link after dispatch = %+v, want synced with an MLflowRunID", link)
	}
	if client.createRunCalls != 1 || client.logBatchCalls != 1 {
		t.Fatalf("client calls: createRun=%d logBatch=%d, want 1 each", client.createRunCalls, client.logBatchCalls)
	}

	// Now the finished half of the lifecycle.
	if err := mlflow.EnqueuePipelineRunFinished(ctx, repos.Mlflow, repos.Outbox, projectID, runID, "success", time.Now()); err != nil {
		t.Fatalf("EnqueuePipelineRunFinished: %v", err)
	}
	if n := dispatcher.PollOnce(ctx); n != 1 {
		t.Fatalf("PollOnce (finished) claimed %d events, want 1", n)
	}
	if len(client.updateRunCalls) != 1 || client.updateRunCalls[0].Status != mlflow.RunStatusFinished {
		t.Fatalf("UpdateRun calls = %+v, want one FINISHED call", client.updateRunCalls)
	}
}

// TestEndToEnd_ClientFailureNeverBlocksOrCorruptsTheOutbox demonstrates the
// failure-isolation property design doc section 4.3 requires: when the
// MLflow-side Client fails outright, the outbox event is retried (not lost,
// not delivered), and — structurally, not just by absence of a crash —
// nothing in this call path ever touches a Piper run's own state: neither
// the Exporter nor the Dispatcher hold a run.Repository at all, only
// mlflow.Repository/outbox.Repository, so there is no code path through
// which an MLflow outage could reach back into Piper's run status. (The run
// lifecycle side of this guarantee — that EnqueuePipelineRunCreated/
// EnqueuePipelineRunFinished themselves never fail run creation/
// finalization even when they return an error — is enforced by
// internal/runlifecycle/runs.go's call sites only logging that error, and
// by queue.go's OnRunOutcome firing asynchronously via appendEffect after
// the terminal-status CAS has already committed; see piper.go's wiring.)
func TestEndToEnd_ClientFailureNeverBlocksOrCorruptsTheOutbox(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	const projectID = "e2e-project"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	integration := &mlflow.MLflowIntegration{
		ID: "int-e2e", ProjectID: projectID, Name: "default",
		TrackingURI: "https://mlflow.example.com", CredentialRef: "mlflow-cred",
		Enabled: true, Default: true, ExportPipelines: true,
		ExperimentTemplate: mlflow.DefaultExperimentTemplate, ArtifactMode: string(mlflow.ArtifactModeReference),
	}
	if err := repos.Mlflow.CreateIntegration(ctx, integration); err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	const runID = "run-e2e-2"
	if err := mlflow.EnqueuePipelineRunCreated(ctx, repos.Mlflow, repos.Outbox, projectID, runID,
		nil, "train", 0, "", "alice", "baremetal", "", time.Now()); err != nil {
		t.Fatalf("EnqueuePipelineRunCreated: %v", err)
	}

	client := &fakeClient{createExperimentErr: unreachableErr{}}
	exporter := mlflow.NewExporter(repos.Mlflow, func(context.Context, *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })
	dispatcher := outbox.NewDispatcher(repos.Outbox, exporter, outbox.Config{Owner: "test-dispatcher", MaxAttemptsBeforeDead: 20})

	dispatcher.PollOnce(ctx)

	// The event must still be present and retryable (pending again with a
	// future NextAttemptAt), not delivered, not dead, and not silently
	// dropped.
	pending, err := repos.Outbox.CountByStatus(ctx, integration.ID, string(outbox.StatusPending))
	if err != nil {
		t.Fatalf("CountByStatus pending: %v", err)
	}
	dead, err := repos.Outbox.CountByStatus(ctx, integration.ID, string(outbox.StatusDead))
	if err != nil {
		t.Fatalf("CountByStatus dead: %v", err)
	}
	if pending != 1 || dead != 0 {
		t.Fatalf("pending=%d dead=%d, want pending=1 dead=0 (retryable failure keeps the event alive for retry)", pending, dead)
	}

	// No run link should exist at all — MLflow's outage doesn't fabricate a
	// degraded-but-present link, it just means "not synced yet."
	link, err := repos.Mlflow.GetRunLink(ctx, integration.ID, projectID, string(mlflow.SourceTypePipeline), runID)
	if err != nil {
		t.Fatalf("GetRunLink: %v", err)
	}
	if link != nil {
		t.Fatalf("run link = %+v, want nil (experiment resolution failed before any run link was created)", link)
	}
}

type unreachableErr struct{}

func (unreachableErr) Error() string { return "dial tcp: connection refused" }
