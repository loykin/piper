package mlflow_test

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/pkg/integration/mlflow"
	"github.com/loykin/piper/pkg/integration/outbox"
)

// fakeRepo is an in-memory mlflow.Repository — the SQLite/Postgres
// conformance tests (internal/store/repotest) already cover the real
// persistence contract; this exists purely to drive Exporter.Handle in
// isolation, fast and without a DB.
type fakeRepo struct {
	mu           sync.Mutex
	integrations map[string]*mlflow.MLflowIntegration
	expLinks     map[string]*mlflow.MLflowExperimentLink
	runLinks     map[string]*mlflow.MLflowRunLink
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		integrations: map[string]*mlflow.MLflowIntegration{},
		expLinks:     map[string]*mlflow.MLflowExperimentLink{},
		runLinks:     map[string]*mlflow.MLflowRunLink{},
	}
}

func (r *fakeRepo) SetSSRFPolicy(mlflow.SSRFPolicy) {}

func (r *fakeRepo) CreateIntegration(ctx context.Context, m *mlflow.MLflowIntegration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.integrations[m.ProjectID+"/"+m.ID] = m
	return nil
}
func (r *fakeRepo) GetIntegration(ctx context.Context, projectID, id string) (*mlflow.MLflowIntegration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.integrations[projectID+"/"+id], nil
}
func (r *fakeRepo) GetIntegrationByName(ctx context.Context, projectID, name string) (*mlflow.MLflowIntegration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.integrations {
		if m.ProjectID == projectID && m.Name == name {
			return m, nil
		}
	}
	return nil, nil
}
func (r *fakeRepo) GetDefaultIntegration(ctx context.Context, projectID string) (*mlflow.MLflowIntegration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.integrations {
		if m.ProjectID == projectID && m.Default {
			return m, nil
		}
	}
	return nil, nil
}
func (r *fakeRepo) ListIntegrations(ctx context.Context, projectID string, limit, offset int) ([]*mlflow.MLflowIntegration, error) {
	return nil, nil
}
func (r *fakeRepo) CountIntegrations(ctx context.Context, projectID string) (int, error) {
	return 0, nil
}
func (r *fakeRepo) UpdateIntegration(ctx context.Context, m *mlflow.MLflowIntegration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.integrations[m.ProjectID+"/"+m.ID] = m
	return nil
}
func (r *fakeRepo) DeleteIntegration(ctx context.Context, projectID, id string) error {
	return nil
}
func (r *fakeRepo) GetExperimentLink(ctx context.Context, integrationID, projectID, key string) (*mlflow.MLflowExperimentLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.expLinks[integrationID+"/"+projectID+"/"+key], nil
}
func (r *fakeRepo) UpsertExperimentLink(ctx context.Context, link *mlflow.MLflowExperimentLink) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *link
	r.expLinks[link.IntegrationID+"/"+link.ProjectID+"/"+link.PiperGroupKey] = &cp
	return nil
}
func (r *fakeRepo) GetRunLink(ctx context.Context, integrationID, projectID, sourceType, sourceID string) (*mlflow.MLflowRunLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	link := r.runLinks[integrationID+"/"+projectID+"/"+sourceType+"/"+sourceID]
	if link == nil {
		return nil, nil
	}
	cp := *link
	return &cp, nil
}
func (r *fakeRepo) UpsertRunLink(ctx context.Context, link *mlflow.MLflowRunLink) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *link
	r.runLinks[link.IntegrationID+"/"+link.ProjectID+"/"+link.SourceType+"/"+link.SourceID] = &cp
	return nil
}
func (r *fakeRepo) ListRunLinksByStatus(ctx context.Context, projectID, status string, limit, offset int) ([]*mlflow.MLflowRunLink, error) {
	return nil, nil
}

func (r *fakeRepo) runLink(integrationID, projectID, sourceID string) *mlflow.MLflowRunLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runLinks[integrationID+"/"+projectID+"/"+string(mlflow.SourceTypePipeline)+"/"+sourceID]
}

// fakeClient is a scriptable mlflow.Client for exporter tests.
type fakeClient struct {
	mu sync.Mutex

	existingExperiment *mlflow.Experiment
	existingRuns       []*mlflow.Run // returned by SearchRuns

	createExperimentErr error
	createRunErr        error
	logBatchErr         error
	updateRunErr        error

	createExperimentCalls int
	createRunCalls        int
	logBatchCalls         int
	updateRunCalls        []mlflow.UpdateRunRequest
	loggedParams          []mlflow.Param
	loggedTags            []mlflow.Tag
}

func (c *fakeClient) GetExperimentByName(ctx context.Context, name string) (*mlflow.Experiment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.existingExperiment, nil
}
func (c *fakeClient) CreateExperiment(ctx context.Context, req mlflow.CreateExperimentRequest) (*mlflow.Experiment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.createExperimentCalls++
	if c.createExperimentErr != nil {
		return nil, c.createExperimentErr
	}
	exp := &mlflow.Experiment{ExperimentID: "exp-1", Name: req.Name}
	c.existingExperiment = exp
	return exp, nil
}
func (c *fakeClient) CreateRun(ctx context.Context, req mlflow.CreateRunRequest) (*mlflow.Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.createRunCalls++
	if c.createRunErr != nil {
		return nil, c.createRunErr
	}
	return &mlflow.Run{RunID: "mlrun-1", ExperimentID: req.ExperimentID, Status: mlflow.RunStatusRunning}, nil
}
func (c *fakeClient) GetRun(ctx context.Context, runID string) (*mlflow.Run, error) {
	return &mlflow.Run{RunID: runID}, nil
}
func (c *fakeClient) SearchRuns(ctx context.Context, req mlflow.SearchRunsRequest) (mlflow.RunPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return mlflow.RunPage{Runs: c.existingRuns}, nil
}
func (c *fakeClient) LogBatch(ctx context.Context, req mlflow.LogBatchRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logBatchCalls++
	c.loggedParams = req.Params
	c.loggedTags = req.Tags
	return c.logBatchErr
}
func (c *fakeClient) UpdateRun(ctx context.Context, req mlflow.UpdateRunRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updateRunCalls = append(c.updateRunCalls, req)
	return c.updateRunErr
}
func (c *fakeClient) UploadArtifact(ctx context.Context, a, b string, r io.Reader, size int64) error {
	return nil
}

func newTestIntegration(projectID string) *mlflow.MLflowIntegration {
	return &mlflow.MLflowIntegration{
		ID:                 uuid.NewString(),
		ProjectID:          projectID,
		Name:               "default",
		TrackingURI:        "https://mlflow.example.com",
		CredentialRef:      "mlflow-cred",
		Enabled:            true,
		Default:            true,
		ExportPipelines:    true,
		ExperimentTemplate: mlflow.DefaultExperimentTemplate,
		ArtifactMode:       string(mlflow.ArtifactModeReference),
	}
}

func newCreatedEvent(t *testing.T, integrationID, projectID, runID string, seq int64) *outbox.Event {
	t.Helper()
	payload := mlflow.PipelineRunCreatedPayload{
		ProjectID: projectID, RunID: runID, PipelineName: "train",
		Params: map[string]any{"epochs": float64(10)}, StartTime: time.Now(), RunURL: "/api/projects/" + projectID + "/runs/" + runID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &outbox.Event{ID: uuid.NewString(), IntegrationID: integrationID, ProjectID: projectID,
		AggregateType: outbox.AggregateTypePipelineRun, AggregateID: runID,
		EventType: mlflow.EventTypePipelineRunCreated, PayloadJSON: body, Sequence: seq}
}

func newFinishedEvent(t *testing.T, integrationID, projectID, runID, status string, seq int64) *outbox.Event {
	t.Helper()
	payload := mlflow.PipelineRunFinishedPayload{ProjectID: projectID, RunID: runID, Status: status, EndTime: time.Now()}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &outbox.Event{ID: uuid.NewString(), IntegrationID: integrationID, ProjectID: projectID,
		AggregateType: outbox.AggregateTypePipelineRun, AggregateID: runID,
		EventType: mlflow.EventTypePipelineRunFinished, PayloadJSON: body, Sequence: seq}
}

func TestExporter_HandlePipelineRunCreated_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	ev := newCreatedEvent(t, integration.ID, "p1", "run-1", 1)
	outcome := exporter.Handle(context.Background(), ev)
	if !outcome.Delivered {
		t.Fatalf("outcome = %+v, want Delivered", outcome)
	}
	if client.createExperimentCalls != 1 || client.createRunCalls != 1 || client.logBatchCalls != 1 {
		t.Fatalf("client calls: createExperiment=%d createRun=%d logBatch=%d", client.createExperimentCalls, client.createRunCalls, client.logBatchCalls)
	}
	link := repo.runLink(integration.ID, "p1", "run-1")
	if link == nil || link.SyncStatus != string(mlflow.SyncStatusSynced) || link.MLflowRunID != "mlrun-1" {
		t.Fatalf("run link = %+v", link)
	}
	foundEpochs := false
	for _, p := range client.loggedParams {
		if p.Key == "epochs" && p.Value == "10" {
			foundEpochs = true
		}
	}
	if !foundEpochs {
		t.Errorf("expected epochs=10 param logged, got %+v", client.loggedParams)
	}
}

func TestExporter_HandlePipelineRunCreated_IdempotentReplayDoesNotRecreate(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	ev := newCreatedEvent(t, integration.ID, "p1", "run-1", 1)
	if o := exporter.Handle(context.Background(), ev); !o.Delivered {
		t.Fatalf("first Handle: %+v", o)
	}
	callsAfterFirst := client.createRunCalls

	// Simulate a redelivered event (lease expired after success, or a
	// duplicate claim) — at-least-once delivery.
	outcome := exporter.Handle(context.Background(), ev)
	if !outcome.Delivered {
		t.Fatalf("replay outcome = %+v, want Delivered", outcome)
	}
	if client.createRunCalls != callsAfterFirst {
		t.Errorf("replay called CreateRun again: %d -> %d", callsAfterFirst, client.createRunCalls)
	}
}

func TestExporter_SearchBeforeCreateDedupe(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{existingRuns: []*mlflow.Run{{RunID: "already-exists", ExperimentID: "exp-1"}}}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	ev := newCreatedEvent(t, integration.ID, "p1", "run-1", 1)
	outcome := exporter.Handle(context.Background(), ev)
	if !outcome.Delivered {
		t.Fatalf("outcome = %+v", outcome)
	}
	if client.createRunCalls != 0 {
		t.Errorf("CreateRun called %d times, want 0 (should have found the existing run via SearchRuns)", client.createRunCalls)
	}
	link := repo.runLink(integration.ID, "p1", "run-1")
	if link == nil || link.MLflowRunID != "already-exists" {
		t.Fatalf("run link = %+v, want MLflowRunID=already-exists", link)
	}
}

func TestExporter_HandlePipelineRunFinished_MapsStatusAndCallsUpdateRun(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	created := newCreatedEvent(t, integration.ID, "p1", "run-1", 1)
	if o := exporter.Handle(context.Background(), created); !o.Delivered {
		t.Fatalf("created: %+v", o)
	}

	finished := newFinishedEvent(t, integration.ID, "p1", "run-1", "success", 2)
	outcome := exporter.Handle(context.Background(), finished)
	if !outcome.Delivered {
		t.Fatalf("finished outcome = %+v", outcome)
	}
	if len(client.updateRunCalls) != 1 || client.updateRunCalls[0].Status != mlflow.RunStatusFinished {
		t.Fatalf("UpdateRun calls = %+v, want one call with status FINISHED", client.updateRunCalls)
	}
}

func TestExporter_HandlePipelineRunFinished_NoRunLinkIsNoOp(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	// No "created" event was ever processed for this run.
	finished := newFinishedEvent(t, integration.ID, "p1", "orphan-run", "success", 1)
	outcome := exporter.Handle(context.Background(), finished)
	if !outcome.Delivered {
		t.Fatalf("outcome = %+v, want Delivered (nothing to finalize)", outcome)
	}
	if len(client.updateRunCalls) != 0 {
		t.Errorf("UpdateRun should not be called when there's no run link, got %+v", client.updateRunCalls)
	}
}

func TestExporter_UnknownRunStatusIsNonRetryable(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	created := newCreatedEvent(t, integration.ID, "p1", "run-1", 1)
	_ = exporter.Handle(context.Background(), created)

	finished := newFinishedEvent(t, integration.ID, "p1", "run-1", "bogus-status", 2)
	outcome := exporter.Handle(context.Background(), finished)
	if outcome.Delivered || outcome.Retryable {
		t.Fatalf("outcome = %+v, want non-retryable failure for an unmapped status", outcome)
	}
}

func TestExporter_DisabledIntegrationParksInsteadOfDeadLettering(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	integration.Enabled = false
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	ev := newCreatedEvent(t, integration.ID, "p1", "run-1", 1)
	outcome := exporter.Handle(context.Background(), ev)
	if outcome.Delivered || !outcome.Retryable || outcome.RetryAfter <= 0 {
		t.Fatalf("outcome = %+v, want retryable-with-delay (parked), not delivered or dead-lettered", outcome)
	}
	if client.createRunCalls != 0 {
		t.Errorf("disabled integration should never reach the client, but CreateRun was called")
	}
}

func TestExporter_ClientErrorIsRetryableWhenClassifiedSo(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	client := &fakeClient{createExperimentErr: &retryableTestErr{}}
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) { return client, nil })

	ev := newCreatedEvent(t, integration.ID, "p1", "run-1", 1)
	outcome := exporter.Handle(context.Background(), ev)
	if outcome.Delivered {
		t.Fatalf("outcome = %+v, want not delivered", outcome)
	}
	if !outcome.Retryable {
		t.Errorf("outcome.Retryable = false, want true for a *ClientError-classified-retryable failure")
	}
}

// TestExporter_NotifyDeadDegradesSyncedRunLink is a regression test for AS:
// once a "created" event synced a run link, a later "finished" event that
// the outbox dispatcher gives up on after exhausting retries (still
// nominally Retryable) used to leave that link at SyncStatus "synced"
// forever — synced and permanently failed at the same time, with nothing
// in the link surfacing the dead event's error. The Dispatcher calls
// NotifyDead exactly for this case (see outbox.DeadNotifier's doc comment).
func TestExporter_NotifyDeadDegradesSyncedRunLink(t *testing.T) {
	repo := newFakeRepo()
	integration := newTestIntegration("p1")
	_ = repo.CreateIntegration(context.Background(), integration)
	now := time.Now()
	_ = repo.UpsertRunLink(context.Background(), &mlflow.MLflowRunLink{
		IntegrationID: integration.ID, ProjectID: "p1", SourceType: string(mlflow.SourceTypePipeline), SourceID: "run-1",
		MLflowRunID: "mlflow-run-1", SyncStatus: string(mlflow.SyncStatusSynced), LastSequence: 1, LastSyncedAt: &now,
	})
	exporter := mlflow.NewExporter(repo, func(ctx context.Context, m *mlflow.MLflowIntegration) (mlflow.Client, error) {
		t.Fatal("NotifyDead must not need a live MLflow client")
		return nil, nil
	})

	ev := newFinishedEvent(t, integration.ID, "p1", "run-1", "success", 2)
	exporter.NotifyDead(context.Background(), ev, outbox.Outcome{Retryable: true, ErrorCode: "NETWORK_ERROR", ErrorMessage: "dial tcp: connection refused"})

	link := repo.runLink(integration.ID, "p1", "run-1")
	if link == nil {
		t.Fatal("run link disappeared")
	}
	if link.SyncStatus != string(mlflow.SyncStatusDegraded) {
		t.Errorf("SyncStatus = %q, want %q — synced-forever alongside a dead event misrepresents state", link.SyncStatus, mlflow.SyncStatusDegraded)
	}
	if link.LastErrorCode != "NETWORK_ERROR" {
		t.Errorf("LastErrorCode = %q, want NETWORK_ERROR", link.LastErrorCode)
	}
	// The prior successful sync's bookkeeping must survive — NotifyDead
	// degrades the link, it doesn't erase what already succeeded.
	if link.MLflowRunID != "mlflow-run-1" || link.LastSequence != 1 {
		t.Errorf("NotifyDead must not clobber existing MLflowRunID/LastSequence, got %+v", link)
	}
}

// retryableTestErr mimics a *mlflow.ClientError-shaped retryable failure
// without depending on http_client.go's unexported construction — it's a
// plain error; Exporter's fallback classification (retryOutcome) treats any
// non-*ClientError as retryable by default (a conservative choice: unknown
// errors get retried with backoff rather than immediately dead-lettered).
type retryableTestErr struct{}

func (e *retryableTestErr) Error() string { return "simulated transient failure" }
