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
	"github.com/piper/piper/internal/proto"
)

type capturingRunRepo struct {
	filter RunFilter
	run    *Run
}

func (r *capturingRunRepo) Create(context.Context, *Run) error { return nil }
func (r *capturingRunRepo) Get(context.Context, string) (*Run, error) {
	return r.run, nil
}
func (r *capturingRunRepo) List(_ context.Context, filter RunFilter) ([]*Run, error) {
	r.filter = filter
	return []*Run{}, nil
}
func (r *capturingRunRepo) UpdateStatus(context.Context, string, string, *time.Time) error {
	return nil
}
func (r *capturingRunRepo) MarkRunning(context.Context, string, time.Time) error {
	return nil
}
func (r *capturingRunRepo) Delete(context.Context, string) error { return nil }
func (r *capturingRunRepo) GetLatestSuccessful(context.Context, string) (*Run, error) {
	return nil, nil
}

type emptyStepRepo struct{}

func (r emptyStepRepo) Upsert(context.Context, *Step) error { return nil }
func (r emptyStepRepo) List(context.Context, string) ([]*Step, error) {
	return []*Step{}, nil
}
func (r emptyStepRepo) DeleteByRun(context.Context, string) error { return nil }

func TestListRunsPipelineNameQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &capturingRunRepo{}
	router := gin.New()
	NewHandler(HandlerDeps{
		Runs:  repo,
		Steps: emptyStepRepo{},
	}).RegisterRoutes(router.Group(""))

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
		StartRun: func(_ context.Context, _ string, _ string, _ map[string]any, _ proto.BuiltinVars, experiment string) (string, error) {
			gotExperiment = experiment
			return "run-1", nil
		},
	}).RegisterRoutes(router.Group(""))

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
	}).RegisterRoutes(router.Group(""))

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
	}).RegisterRoutes(router.Group(""))

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
	}).RegisterRoutes(router.Group(""))

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
	}).RegisterRoutes(router.Group(""))

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
