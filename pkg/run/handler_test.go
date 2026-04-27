package run

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

	req := httptest.NewRequest(http.MethodGet, "/runs?pipeline_name=train&status=success", nil)
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
