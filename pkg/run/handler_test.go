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
}

func (r *capturingRunRepo) Create(context.Context, *Run) error { return nil }
func (r *capturingRunRepo) Get(context.Context, string) (*Run, error) {
	return nil, nil
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
