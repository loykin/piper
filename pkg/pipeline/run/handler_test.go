package run

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
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

// fakeMemberClient is a configurable memberclient.Client double for
// handler-level tests. Zero-value fields fall back to safe defaults so
// tests only need to set the hooks/fields they actually exercise (mirrors
// the fakeDriver pattern used elsewhere in this codebase).
type fakeMemberClient struct {
	runOK     bool
	run       memberclient.RunSummary
	steps     []memberclient.StepSummary
	runs      []memberclient.RunSummary
	runsSteps map[string][]memberclient.StepSummary

	listRunsReq memberclient.ListRunsRequest

	submitRunFn   func(ctx context.Context, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error)
	submitSweepFn func(ctx context.Context, req memberclient.SubmitSweepRequest) (memberclient.SubmitSweepResponse, error)
	cancelRunFn   func(ctx context.Context, runID string) error
	rerunRunFn    func(ctx context.Context, runID string, failedOnly bool) (string, error)
	deleteRunFn   func(ctx context.Context, runID string) error
	retryStepFn   func(ctx context.Context, runID, stepName string) (string, error)
}

func (f *fakeMemberClient) SubmitRun(ctx context.Context, _ memberclient.AuthContext, _ project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
	if f.submitRunFn != nil {
		return f.submitRunFn(ctx, req)
	}
	return memberclient.SubmitRunResponse{RunID: "run-1"}, nil
}

func (f *fakeMemberClient) SubmitSweep(ctx context.Context, _ memberclient.AuthContext, _ project.ProjectRef, req memberclient.SubmitSweepRequest) (memberclient.SubmitSweepResponse, error) {
	if f.submitSweepFn != nil {
		return f.submitSweepFn(ctx, req)
	}
	return memberclient.SubmitSweepResponse{Experiment: req.Experiment}, nil
}

func (f *fakeMemberClient) ListRuns(_ context.Context, _ memberclient.AuthContext, _ project.ProjectRef, req memberclient.ListRunsRequest) (memberclient.ListRunsResponse, error) {
	f.listRunsReq = req
	resp := memberclient.ListRunsResponse{Runs: f.runs}
	if req.Limit > 0 {
		resp.Total = len(f.runs)
	}
	if req.IncludeSteps {
		resp.Steps = f.runsSteps
	}
	return resp, nil
}

func (f *fakeMemberClient) GetRun(context.Context, memberclient.AuthContext, project.ProjectRef, string) (memberclient.RunDetail, error) {
	if !f.runOK {
		return memberclient.RunDetail{}, memberclient.ErrRunNotFound
	}
	return memberclient.RunDetail{Run: f.run, Steps: f.steps}, nil
}

func (f *fakeMemberClient) CancelRun(ctx context.Context, _ memberclient.AuthContext, _ project.ProjectRef, runID string) error {
	if f.cancelRunFn != nil {
		return f.cancelRunFn(ctx, runID)
	}
	return nil
}

func (f *fakeMemberClient) RerunRun(ctx context.Context, _ memberclient.AuthContext, _ project.ProjectRef, runID string, failedOnly bool) (string, error) {
	if f.rerunRunFn != nil {
		return f.rerunRunFn(ctx, runID, failedOnly)
	}
	return "run-2", nil
}

func (f *fakeMemberClient) DeleteRun(ctx context.Context, _ memberclient.AuthContext, _ project.ProjectRef, runID string) error {
	if f.deleteRunFn != nil {
		return f.deleteRunFn(ctx, runID)
	}
	return nil
}

func (f *fakeMemberClient) ListSteps(context.Context, memberclient.AuthContext, project.ProjectRef, string) ([]memberclient.StepSummary, error) {
	return f.steps, nil
}

func (f *fakeMemberClient) RetryStep(ctx context.Context, _ memberclient.AuthContext, _ project.ProjectRef, runID, stepName string) (string, error) {
	if f.retryStepFn != nil {
		return f.retryStepFn(ctx, runID, stepName)
	}
	return "run-3", nil
}

func (f *fakeMemberClient) QueryLogs(context.Context, memberclient.AuthContext, project.ProjectRef, string, string, int64) ([]*logstore.Line, error) {
	return nil, nil
}

func (f *fakeMemberClient) QueryMetrics(context.Context, memberclient.AuthContext, project.ProjectRef, string, string) ([]*logstore.Metric, error) {
	return nil, nil
}

func (f *fakeMemberClient) ListArtifacts(context.Context, memberclient.AuthContext, project.ProjectRef, string) ([]any, error) {
	return nil, nil
}

func (f *fakeMemberClient) ServeArtifact(context.Context, memberclient.AuthContext, project.ProjectRef, http.ResponseWriter, *http.Request, string, string, string) {
}

var _ memberclient.Client = (*fakeMemberClient)(nil)

// ── metric filter ─────────────────────────────────────────────────────────────

func TestListRunsMetricFilterPassedToRepo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	member := &fakeMemberClient{}
	router := gin.New()
	NewHandler(HandlerDeps{Member: member, ProjectRef: project.LocalRef}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodGet, "/runs?experiment=sweep-1&metric_step=train&metric_key=accuracy&metric_order=asc", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if member.listRunsReq.MetricStep != "train" {
		t.Errorf("MetricStep = %q, want train", member.listRunsReq.MetricStep)
	}
	if member.listRunsReq.MetricKey != "accuracy" {
		t.Errorf("MetricKey = %q, want accuracy", member.listRunsReq.MetricKey)
	}
	if member.listRunsReq.MetricOrder != "asc" {
		t.Errorf("MetricOrder = %q, want asc", member.listRunsReq.MetricOrder)
	}
}

func TestMemberUnavailableReturnsServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	member := &memberclient.RoutingClient{Resolve: func(project.ProjectRef) (memberclient.Client, error) {
		return nil, fmt.Errorf("member-1: %w", memberclient.ErrMemberUnavailable)
	}}
	NewHandler(HandlerDeps{Member: member, ProjectRef: project.LocalRef}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodGet, "/runs/run-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

// ── sweep ─────────────────────────────────────────────────────────────────────

func TestCreateSweep_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotReq memberclient.SubmitSweepRequest
	router := gin.New()
	NewHandler(HandlerDeps{
		Member: &fakeMemberClient{
			submitSweepFn: func(_ context.Context, req memberclient.SubmitSweepRequest) (memberclient.SubmitSweepResponse, error) {
				gotReq = req
				return memberclient.SubmitSweepResponse{Experiment: req.Experiment, RunIDs: []string{"r1", "r2"}}, nil
			},
		},
		ProjectRef: project.LocalRef,
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	body := `{"yaml":"metadata:\n  name: train\n","experiment":"lr-sweep","runs":[{"params":{"lr":0.01}},{"params":{"lr":0.1}}]}`
	req := httptest.NewRequest(http.MethodPost, "/runs/sweep", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
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
	NewHandler(HandlerDeps{Member: &fakeMemberClient{}, ProjectRef: project.LocalRef}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

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
	NewHandler(HandlerDeps{Member: &fakeMemberClient{}, ProjectRef: project.LocalRef}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	body := `{"yaml":"...","experiment":"lr-sweep","runs":[]}`
	req := httptest.NewRequest(http.MethodPost, "/runs/sweep", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListRunsPipelineNameQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	member := &fakeMemberClient{}
	router := gin.New()
	NewHandler(HandlerDeps{Member: member, ProjectRef: project.LocalRef}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodGet, "/runs?pipeline_name=train&status=success&experiment=exp-v2", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if member.listRunsReq.PipelineName != "train" {
		t.Fatalf("PipelineName = %q, want train", member.listRunsReq.PipelineName)
	}
	if member.listRunsReq.Status != "success" {
		t.Fatalf("Status = %q, want success", member.listRunsReq.Status)
	}
	if member.listRunsReq.Experiment != "exp-v2" {
		t.Fatalf("Experiment = %q, want exp-v2", member.listRunsReq.Experiment)
	}
}

func TestListRunsScheduleFilterAndIncludeSteps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	member := &fakeMemberClient{
		runs: []memberclient.RunSummary{{
			ID:           "run-1",
			ProjectID:    "test-proj",
			ScheduleID:   "sch-1",
			PipelineName: "train",
			Status:       StatusSuccess,
		}},
		runsSteps: map[string][]memberclient.StepSummary{
			"run-1": {{RunID: "run-1", StepName: "fit", Status: "done", Attempts: 1}},
		},
	}
	router := gin.New()
	NewHandler(HandlerDeps{Member: member, ProjectRef: project.LocalRef}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodGet, "/runs?schedule_id=sch-1&include_steps=true", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if member.listRunsReq.ScheduleID != "sch-1" {
		t.Fatalf("ScheduleID = %q, want sch-1", member.listRunsReq.ScheduleID)
	}
	if !member.listRunsReq.IncludeSteps {
		t.Fatal("IncludeSteps = false, want true")
	}
	if !strings.Contains(rec.Body.String(), `"steps":[`) {
		t.Fatalf("response did not include steps: %s", rec.Body.String())
	}
}

func TestListRunsDefaultOmitsSteps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	member := &fakeMemberClient{
		runs: []memberclient.RunSummary{{ID: "run-1", ProjectID: "test-proj", PipelineName: "train", Status: StatusSuccess}},
		runsSteps: map[string][]memberclient.StepSummary{
			"run-1": {{RunID: "run-1", StepName: "fit", Status: "done", Attempts: 1}},
		},
	}
	router := gin.New()
	NewHandler(HandlerDeps{Member: member, ProjectRef: project.LocalRef}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if member.listRunsReq.IncludeSteps {
		t.Fatal("IncludeSteps = true, want false")
	}
	if strings.Contains(rec.Body.String(), `"steps"`) {
		t.Fatalf("response should omit steps by default: %s", rec.Body.String())
	}
}

func TestCreateRunPassesExperiment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotExperiment string
	var gotIdempotencyKey string
	router := gin.New()
	NewHandler(HandlerDeps{
		Member: &fakeMemberClient{
			submitRunFn: func(_ context.Context, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
				gotExperiment = req.Experiment
				gotIdempotencyKey = req.IdempotencyKey
				return memberclient.SubmitRunResponse{RunID: "run-1"}, nil
			},
		},
		ProjectRef: project.LocalRef,
	}).RegisterRoutes(router.Group("", injectProjectContext("test-proj")))

	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{"yaml":"metadata:\n  name: train\n","experiment":"exp-v2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "request-123")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if gotExperiment != "exp-v2" {
		t.Fatalf("experiment = %q, want exp-v2", gotExperiment)
	}
	if gotIdempotencyKey != "request-123" || rec.Header().Get("Idempotency-Key") != "request-123" {
		t.Fatalf("idempotency key request=%q response=%q", gotIdempotencyKey, rec.Header().Get("Idempotency-Key"))
	}
}

func TestCancelRunUsesCancelDependency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	canceled := ""
	router := gin.New()
	NewHandler(HandlerDeps{
		Member: &fakeMemberClient{
			runOK: true,
			run:   memberclient.RunSummary{ID: "run-1", Status: StatusRunning},
			cancelRunFn: func(_ context.Context, runID string) error {
				canceled = runID
				return nil
			},
		},
		ProjectRef: project.LocalRef,
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
	var gotRunID string
	var gotFailedOnly bool
	router := gin.New()
	NewHandler(HandlerDeps{
		Member: &fakeMemberClient{
			runOK: true,
			run:   memberclient.RunSummary{ID: "run-1", Status: StatusFailed},
			rerunRunFn: func(_ context.Context, runID string, failedOnly bool) (string, error) {
				gotRunID = runID
				gotFailedOnly = failedOnly
				return "run-2", nil
			},
		},
		ProjectRef: project.LocalRef,
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
	deleted := ""
	router := gin.New()
	NewHandler(HandlerDeps{
		Member: &fakeMemberClient{
			runOK: true,
			run:   memberclient.RunSummary{ID: "run-1", Status: StatusFailed},
			deleteRunFn: func(_ context.Context, runID string) error {
				deleted = runID
				return nil
			},
		},
		ProjectRef: project.LocalRef,
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
	deleteCalled := false
	router := gin.New()
	NewHandler(HandlerDeps{
		Member: &fakeMemberClient{
			runOK: true,
			run:   memberclient.RunSummary{ID: "run-1", Status: StatusFailed},
			deleteRunFn: func(_ context.Context, _ string) error {
				deleteCalled = true
				return nil
			},
		},
		ProjectRef: project.LocalRef,
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
