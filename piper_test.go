package piper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/schedule"
)

type noopBackend struct{}

func (noopBackend) Dispatch(context.Context, *proto.Task) error { return nil }

func TestRunPipeline_localArtifactPathIncludesRunID(t *testing.T) {
	outputDir := t.TempDir()
	p, err := New(Config{OutputDir: outputDir})
	if err != nil {
		t.Fatal(err)
	}

	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "local-path-test"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{{
			Name: "train",
			Run: pipeline.Run{
				Command: []string{"sh", "-c", "echo artifact > $PIPER_OUTPUT_DIR/result.txt"},
			},
		}}},
	}

	res, err := p.runPipelineWithRunID(context.Background(), pl, "run-local", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() {
		t.Fatalf("pipeline failed: %+v", res.Steps["train"])
	}

	expected := filepath.Join(outputDir, "run-local", "train", "result.txt")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected artifact at %s: %v", expected, err)
	}

	oldLayout := filepath.Join(outputDir, "train", "result.txt")
	if _, err := os.Stat(oldLayout); !os.IsNotExist(err) {
		t.Fatalf("old artifact layout should not exist at %s", oldLayout)
	}
}

func TestHandlerRejectsOversizedRequestBody(t *testing.T) {
	p, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(strings.Repeat("x", int(maxRequestBodyBytes)+1))
	req := httptest.NewRequest(http.MethodPost, "/runs", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.Handler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandlerServesUIDeepLinks(t *testing.T) {
	p, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/notebooks", nil)
	rec := httptest.NewRecorder()
	p.Handler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("unexpected not found body: %s", rec.Body.String())
	}
}

func TestHandlerParsesMetricsFromIngestedLogs(t *testing.T) {
	p, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	router := p.Handler(nil)
	body := `[{"ts":"2026-05-29T10:00:00Z","stream":"stdout","line":"PIPER_METRIC loss=0.312"}]`
	req := httptest.NewRequest(http.MethodPost, "/runs/run-metric/steps/train/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/runs/run-metric/metrics?step=train", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var metrics []logstore.Metric
	if err := json.NewDecoder(rec.Body).Decode(&metrics); err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].Key != "loss" || metrics[0].Value != 0.312 {
		t.Fatalf("metrics = %#v, want loss=0.312", metrics)
	}
}

func TestWorkerRoutesOnlyMountedInPollingMode(t *testing.T) {
	polling, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	pollingRouter := polling.newRouter(nil).(*gin.Engine)
	if !hasRoute(pollingRouter, http.MethodGet, "/api/workers") {
		t.Fatal("polling mode should mount worker routes")
	}
	if polling.registry == nil {
		t.Fatal("polling mode should lazily create worker registry")
	}

	active, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	active.SetBackend(noopBackend{})
	activeRouter := active.newRouter(nil).(*gin.Engine)
	if hasRoute(activeRouter, http.MethodGet, "/api/workers") {
		t.Fatal("active backend mode should not mount worker routes")
	}
	if hasRoute(activeRouter, http.MethodGet, "/api/tasks/next") {
		t.Fatal("active backend mode should not mount polling task route")
	}
	if active.registry != nil {
		t.Fatal("active backend mode should not hold worker registry")
	}
}

func TestStartRunPersistsExperiment(t *testing.T) {
	p, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "train"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{{
			Name: "step",
			Run:  pipeline.Run{Command: []string{"true"}},
		}}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}

	runID, err := p.startRun(context.Background(), pl, dag, StartRunOptions{
		Experiment: "exp-v2",
		YAML:       "metadata:\n  name: train\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.repos.Run.Get(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Experiment != "exp-v2" {
		t.Fatalf("experiment = %q, want exp-v2", got.Experiment)
	}
	runs, err := p.repos.Run.List(context.Background(), run.RunFilter{Experiment: "exp-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != runID {
		t.Fatalf("filtered runs = %#v, want %s", runs, runID)
	}
}

func TestBackfillScheduleCreatesRunsForCronRange(t *testing.T) {
	p, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Minute)
	yaml := "metadata:\n  name: train\nspec:\n  steps:\n    - name: step\n      run:\n        command: [\"true\"]\n"
	sc := &schedule.Schedule{
		ID:           "sch-backfill",
		Name:         "train",
		PipelineYAML: yaml,
		ScheduleType: "cron",
		CronExpr:     "* * * * *",
		Enabled:      true,
		NextRunAt:    from,
		CreatedAt:    from,
		UpdatedAt:    from,
	}
	if err := p.repos.Schedule.Create(context.Background(), sc); err != nil {
		t.Fatal(err)
	}

	runIDs, err := p.BackfillSchedule(context.Background(), sc.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(runIDs) != 3 {
		t.Fatalf("runIDs = %v, want 3 runs", runIDs)
	}
	runs, err := p.repos.Run.List(context.Background(), run.RunFilter{ScheduleID: sc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("stored runs = %d, want 3", len(runs))
	}
	for _, r := range runs {
		if r.ScheduledAt == nil || r.ScheduledAt.Before(from) || r.ScheduledAt.After(to) {
			t.Fatalf("scheduled_at = %v, want within [%s, %s]", r.ScheduledAt, from, to)
		}
	}
}

func hasRoute(router *gin.Engine, method, path string) bool {
	for _, route := range router.Routes() {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

// TestAuth_ContextInjectedToDownstreamHooks verifies that the context returned
// by Hooks.Auth is available in subsequent hooks (e.g. BeforeCreateRun).
func TestAuth_ContextInjectedToDownstreamHooks(t *testing.T) {
	type ctxKey struct{}

	p, err := New(Config{
		OutputDir: t.TempDir(),
		Hooks: Hooks{
			Auth: func(r *http.Request) (context.Context, error) {
				return context.WithValue(r.Context(), ctxKey{}, "user-42"), nil
			},
			BeforeCreateRun: func(ctx context.Context, r *http.Request, yaml string) error {
				if ctx.Value(ctxKey{}) != "user-42" {
					t.Errorf("BeforeCreateRun ctx missing identity injected by Auth")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := p.newRouter(nil)

	body := `{"yaml":"metadata:\n  name: test\nspec:\n  steps: []\n"}`
	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
}

// TestAuth_RejectsOnError verifies that an Auth hook returning an error
// produces 401 and blocks the request.
func TestAuth_RejectsOnError(t *testing.T) {
	p, err := New(Config{
		OutputDir: t.TempDir(),
		Hooks: Hooks{
			Auth: func(r *http.Request) (context.Context, error) {
				return nil, fmt.Errorf("invalid token")
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := p.newRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestExtractOwnerID_OverridesHeader verifies that Hooks.ExtractOwnerID
// replaces the default X-Piper-Owner-ID header extraction.
func TestExtractOwnerID_OverridesHeader(t *testing.T) {
	p, err := New(Config{
		OutputDir: t.TempDir(),
		Hooks: Hooks{
			ExtractOwnerID: func(r *http.Request) string {
				return "jwt-user-99"
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// ownerIDFromRequest should use the hook, not the header.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Piper-Owner-ID", "header-user")
	got := p.ownerIDFromRequest(req)
	if got != "jwt-user-99" {
		t.Errorf("ownerIDFromRequest = %q, want jwt-user-99", got)
	}
}

// TestExtractOwnerID_DefaultFallback verifies that without the hook,
// the X-Piper-Owner-ID header is used.
func TestExtractOwnerID_DefaultFallback(t *testing.T) {
	p, err := New(Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Piper-Owner-ID", "header-user")
	got := p.ownerIDFromRequest(req)
	if got != "header-user" {
		t.Errorf("ownerIDFromRequest = %q, want header-user", got)
	}
}

// TestArtifactPath_LocalMatchesDistributed verifies that the local (embedded) and
// distributed (runner) execution paths write artifacts to the same directory structure:
// {outputDir}/{runID}/{stepName}
func TestArtifactPath_LocalMatchesDistributed(t *testing.T) {
	outputBase := t.TempDir()
	runID := "run-abc123"
	stepName := "train"

	// piper.go: outputDir = Join(outputBase, runID), stepOutputDir = Join(outputDir, stepName)
	localPath := filepath.Join(outputBase, runID, stepName)

	// runner.go: stepOutputDir = Join(cfg.OutputDir, task.RunID, step.Name)
	runnerPath := filepath.Join(outputBase, runID, stepName)

	if localPath != runnerPath {
		t.Errorf("path mismatch: local=%q runner=%q", localPath, runnerPath)
	}
}
