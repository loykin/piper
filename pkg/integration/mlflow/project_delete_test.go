package mlflow_test

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/integration/mlflow"
	"github.com/loykin/piper/pkg/project"
)

// Deleting a project cascades to its MLflow integrations; outbox events
// still queued for those integrations must not block that cascade.
func TestProjectDeleteWithQueuedOutboxEvents(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	const projectID = "doomed"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	integration := &mlflow.MLflowIntegration{
		ID: "int-1", ProjectID: projectID, Name: "default",
		TrackingURI: "https://mlflow.example.com", CredentialRef: "mlflow-cred",
		Enabled: true, Default: true, ExportPipelines: true,
		ExperimentTemplate: mlflow.DefaultExperimentTemplate, ArtifactMode: string(mlflow.ArtifactModeReference),
	}
	if err := repos.Mlflow.CreateIntegration(ctx, integration); err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if err := mlflow.EnqueuePipelineRunCreated(ctx, repos.Mlflow, repos.Outbox, projectID, "run-1",
		nil, "train", 1, "", "alice", "baremetal", "/api/projects/"+projectID+"/runs/run-1", time.Now()); err != nil {
		t.Fatalf("EnqueuePipelineRunCreated: %v", err)
	}

	if err := repos.Project.Delete(ctx, projectID); err != nil {
		t.Fatalf("project delete blocked by queued outbox events: %v", err)
	}
}
