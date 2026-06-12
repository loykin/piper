package run

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
)

// injectProjectContext is a test middleware that injects a project context with admin role.
func injectProjectContext(id string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{
			ID:   id,
			Role: security.ProjectRoleAdmin,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

type capturingRunRepo struct {
	filter RunFilter
	run    *Run
}

func (r *capturingRunRepo) Create(context.Context, *Run) error { return nil }
func (r *capturingRunRepo) Get(context.Context, string, string) (*Run, error) {
	return r.run, nil
}
func (r *capturingRunRepo) List(_ context.Context, _ string, filter RunFilter) ([]*Run, error) {
	r.filter = filter
	return []*Run{}, nil
}
func (r *capturingRunRepo) UpdateStatus(context.Context, string, string, string, *time.Time) error {
	return nil
}
func (r *capturingRunRepo) MarkRunning(context.Context, string, string, time.Time) error {
	return nil
}
func (r *capturingRunRepo) Delete(context.Context, string, string) error { return nil }
func (r *capturingRunRepo) GetLatestSuccessful(context.Context, string, string) (*Run, error) {
	return nil, nil
}

type emptyStepRepo struct{}

func (r emptyStepRepo) Upsert(context.Context, *Step) error { return nil }
func (r emptyStepRepo) List(context.Context, string, string) ([]*Step, error) {
	return []*Step{}, nil
}
func (r emptyStepRepo) DeleteByRun(context.Context, string, string) error { return nil }

// ── metric filter ─────────────────────────────────────────────────────────────

func TestListRunsMetricFilterPassedToRepo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{}
	router := gin.New()
	NewHandler(HandlerDeps{Runs: repo, Steps: emptyStepRepo{}}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodGet, "/runs?experiment=sweep-1&metric_step=train&metric_key=accuracy&metric_order=asc", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if repo.filter.MetricStep != "train" {
		t.Errorf("MetricStep = %q, want train", repo.filter.MetricStep)
	}
	if repo.filter.MetricKey != "accuracy" {
		t.Errorf("MetricKey = %q, want accuracy", repo.filter.MetricKey)
	}
	if repo.filter.MetricOrder != "asc" {
		t.Errorf("MetricOrder = %q, want asc", repo.filter.MetricOrder)
	}
}

// ── sweep ─────────────────────────────────────────────────────────────────────

func TestCreateSweep_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{}
	var gotReq SweepRequest
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
		StartSweep: func(_ context.Context, req SweepRequest) (SweepResponse, error) {
			gotReq = req
			return SweepResponse{Experiment: req.Experiment, RunIDs: []string{"r1", "r2"}}, nil
		},
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	body := `{"yaml":"metadata:\n  name: train\n","experiment":"lr-sweep","runs":[{"params":{"lr":0.01}},{"params":{"lr":0.1}}]}`
	req := httptest.NewRequest(http.MethodPost, "/runs/sweep", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if gotReq.Experiment != "lr-sweep" {
		t.Errorf("experiment = %q, want lr-sweep", gotReq.Experiment)
	}
	if len(gotReq.Runs) != 2 {
		t.Errorf("trials = %d, want 2", len(gotReq.Runs))
	}
	if rec.Body.String() != `{"experiment":"lr-sweep","run_ids":["r1","r2"]}` {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestCreateSweep_MissingExperiment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(HandlerDeps{Runs: &capturingRunRepo{}, Steps: emptyStepRepo{}}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	body := `{"yaml":"...","runs":[{"params":{"lr":0.01}}]}`
	req := httptest.NewRequest(http.MethodPost, "/runs/sweep", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateSweep_EmptyRuns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(HandlerDeps{Runs: &capturingRunRepo{}, Steps: emptyStepRepo{}}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	body := `{"yaml":"...","experiment":"lr-sweep","runs":[]}`
	req := httptest.NewRequest(http.MethodPost, "/runs/sweep", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ── final-metrics ─────────────────────────────────────────────────────────────

type captureMetricStore struct {
	appended []*logstore.Metric
}

func (s *captureMetricStore) AppendMetrics(m []*logstore.Metric) error {
	s.appended = append(s.appended, m...)
	return nil
}
func (s *captureMetricStore) QueryMetrics(_, _, _ string) ([]*logstore.Metric, error) {
	return nil, nil
}

func TestIngestFinalMetrics_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &captureMetricStore{}
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:    &capturingRunRepo{run: &Run{ID: "run-1", Status: StatusRunning}},
		Steps:   emptyStepRepo{},
		Metrics: store,
	}).RegisterWorkerRoutes(router.Group("", injectProjectContext("test-proj")))

	body := `{"accuracy":0.94,"val_loss":0.23}`
	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/steps/train/final-metrics", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(store.appended) != 2 {
		t.Fatalf("appended %d metrics, want 2", len(store.appended))
	}
	byKey := make(map[string]float64)
	for _, m := range store.appended {
		if m.RunID != "run-1" || m.StepName != "train" {
			t.Errorf("unexpected run/step: %s/%s", m.RunID, m.StepName)
		}
		byKey[m.Key] = m.Value
	}
	if byKey["accuracy"] != 0.94 {
		t.Errorf("accuracy = %v, want 0.94", byKey["accuracy"])
	}
	if byKey["val_loss"] != 0.23 {
		t.Errorf("val_loss = %v, want 0.23", byKey["val_loss"])
	}
}

func TestIngestFinalMetrics_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:    &capturingRunRepo{run: &Run{ID: "run-1", Status: StatusRunning}},
		Steps:   emptyStepRepo{},
		Metrics: &captureMetricStore{},
	}).RegisterWorkerRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/steps/train/final-metrics", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListRunsPipelineNameQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{}
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodGet, "/runs?pipeline_name=train&status=success&experiment=exp-v2", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if repo.filter.PipelineName != "train" {
		t.Fatalf("PipelineName = %q, want train", repo.filter.PipelineName)
	}
	if repo.filter.Status != "success" {
		t.Fatalf("Status = %q, want success", repo.filter.Status)
	}
	if repo.filter.Experiment != "exp-v2" {
		t.Fatalf("Experiment = %q, want exp-v2", repo.filter.Experiment)
	}
}

func TestCreateRunPassesExperiment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{}
	var gotExperiment string
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
		StartRun: func(_ context.Context, _ string, _ map[string]any, _ proto.BuiltinVars, experiment string) (string, error) {
			gotExperiment = experiment
			return "run-1", nil
		},
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{"yaml":"metadata:\n  name: train\n","experiment":"exp-v2"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotExperiment != "exp-v2" {
		t.Fatalf("experiment = %q, want exp-v2", gotExperiment)
	}
}

func TestCancelRunUsesCancelDependency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{run: &Run{ID: "run-1", Status: StatusRunning}}
	canceled := ""
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
		CancelRun: func(_ context.Context, runID string) error {
			canceled = runID
			return nil
		},
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if canceled != "run-1" {
		t.Fatalf("canceled run = %q, want run-1", canceled)
	}
}

func TestRerunUsesRerunDependency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{run: &Run{ID: "run-1", Status: StatusFailed}}
	var gotRunID string
	var gotFailedOnly bool
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
		RerunRun: func(_ context.Context, runID string, failedOnly bool) (string, error) {
			gotRunID = runID
			gotFailedOnly = failedOnly
			return "run-2", nil
		},
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/rerun", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotRunID != "run-1" || gotFailedOnly {
		t.Fatalf("rerun args = %q, %v; want run-1, false", gotRunID, gotFailedOnly)
	}
}

// stubRunHooks is a configurable RunHooks for testing hook dispatch.
type stubRunHooks struct {
	beforeGetRun func(ctx context.Context, r *http.Request, id string) error
}

func (h *stubRunHooks) BeforeListRuns(_ context.Context, _ *http.Request) (RunFilter, error) {
	return RunFilter{}, nil
}
func (h *stubRunHooks) BeforeCreateRun(_ context.Context, _ *http.Request, _ string) error {
	return nil
}
func (h *stubRunHooks) BeforeGetRun(ctx context.Context, r *http.Request, id string) error {
	if h.beforeGetRun != nil {
		return h.beforeGetRun(ctx, r, id)
	}
	return nil
}
func (h *stubRunHooks) BeforeGetLogs(_ context.Context, _ *http.Request, _, _ string) error {
	return nil
}

func TestDeleteRun_CallsBeforeGetRunHook(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hookCalled := ""
	repo := &capturingRunRepo{run: &Run{ID: "run-1", Status: StatusFailed}}
	deleted := ""
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
		DeleteRun: func(_ context.Context, runID string) error {
			deleted = runID
			return nil
		},
		Hooks: &stubRunHooks{
			beforeGetRun: func(_ context.Context, _ *http.Request, id string) error {
				hookCalled = id
				return nil
			},
		},
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodDelete, "/runs/run-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body)
	}
	if hookCalled != "run-1" {
		t.Errorf("BeforeGetRun hook not called with run-1, got %q", hookCalled)
	}
	if deleted != "run-1" {
		t.Errorf("DeleteRun not called with run-1, got %q", deleted)
	}
}

func TestDeleteRun_HookBlocksDeletion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{run: &Run{ID: "run-1", Status: StatusFailed}}
	deleteCalled := false
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
		DeleteRun: func(_ context.Context, _ string) error {
			deleteCalled = true
			return nil
		},
		Hooks: &stubRunHooks{
			beforeGetRun: func(_ context.Context, _ *http.Request, _ string) error {
				return fmt.Errorf("forbidden")
			},
		},
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodDelete, "/runs/run-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if deleteCalled {
		t.Error("DeleteRun should not be called when hook blocks")
	}
}
