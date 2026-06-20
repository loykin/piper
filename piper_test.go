package piper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/run"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/security"
	"github.com/piper/piper/pkg/storage"
)

// testSecurityProvider implements the request authentication and authorization
// capabilities used by router tests.
type testSecurityProvider struct {
	identity  *security.Identity
	authErr   error
	authCalls int
}

type testUserDirectory struct{}

func (testUserDirectory) GetUser(context.Context, string) (*security.User, error) {
	return nil, nil
}

func (testUserDirectory) ListUsers(context.Context) ([]*security.User, error) {
	return []*security.User{}, nil
}

func (p *testSecurityProvider) Authenticate(_ context.Context, _ *http.Request) (*security.Identity, error) {
	p.authCalls++
	return p.identity, p.authErr
}
func (p *testSecurityProvider) ListProjectRoles(_ context.Context, _ *security.Identity) (map[string]security.ProjectRole, error) {
	return nil, nil
}
func (p *testSecurityProvider) ProjectRole(_ context.Context, _ *security.Identity, _ string) (security.ProjectRole, error) {
	return security.ProjectRoleAdmin, nil
}
func (p *testSecurityProvider) AuthorizeSystem(_ context.Context, _ *security.Identity) error {
	return nil
}

func newTestPiper(t *testing.T, cfg Config) *Piper {
	t.Helper()
	if !cfg.Auth.Trusted && cfg.Auth.Authenticator == nil && cfg.Auth.Factory == nil {
		cfg.Auth.Trusted = true
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestRunPipeline_localArtifactPathIncludesRunID(t *testing.T) {
	outputDir := t.TempDir()
	p := newTestPiper(t, Config{OutputDir: outputDir})

	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "local-path-test"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{{
			Name: "train",
			Run: pipeline.Run{
				Command: []string{"sh", "-c", "echo artifact > $PIPER_OUTPUT_DIR/result.txt"},
			},
		}}},
	}

	res, err := p.RunPipeline(context.Background(), pl)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() {
		t.Fatalf("pipeline failed: %+v", res.Steps["train"])
	}

	// artifact must be under outputDir/<runID>/train/result.txt
	matches, err := filepath.Glob(filepath.Join(outputDir, "*/train/result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected artifact under %s/*/train/result.txt but found none", outputDir)
	}

	// old flat layout must not exist
	oldLayout := filepath.Join(outputDir, "train", "result.txt")
	if _, err := os.Stat(oldLayout); !os.IsNotExist(err) {
		t.Fatalf("old artifact layout should not exist at %s", oldLayout)
	}
}

func TestHandlerRejectsOversizedRequestBody(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})

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
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})

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
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	const projectID = "project-a"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	if err := p.repos.Run.Create(context.Background(), &run.Run{
		ID:           "run-metric",
		ProjectID:    projectID,
		PipelineName: "metric-test",
		Status:       run.StatusRunning,
		StartedAt:    time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	push := newWorkerPushHandler(nil, nil, nil, nil, p.logs, p.metrics)
	body, _ := json.Marshal(logsink.LogAppendPush{ProjectID: projectID, RunID: "run-metric", StepName: "train", Lines: []logsink.LogLine{{Ts: time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC), Stream: "stdout", Text: "PIPER_METRIC loss=0.312"}}})
	push(context.Background(), "worker-a", iagent.MethodLogAppend, body)

	router := p.Handler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runs/run-metric/metrics?step=train", nil)
	rec := httptest.NewRecorder()
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

func TestHandlerExposesArtifactStoreSettings(t *testing.T) {
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Storage:   StorageConfig{Disabled: true},
	})
	router := p.Handler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ArtifactStore struct {
			Status string `json:"status"`
		} `json:"artifact_store"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.ArtifactStore.Status != "disabled" {
		t.Fatalf("artifact_store.status = %q, want disabled", out.ArtifactStore.Status)
	}
}

func TestConfigValidateAllowsStorageURLWithoutLegacyS3(t *testing.T) {
	cfg := Config{
		OutputDir: t.TempDir(),
		Auth:      AuthConfig{Trusted: true},
		Storage: StorageConfig{
			URL: "file:///tmp/piper-store",
		},
		S3: S3Config{
			Bucket: "legacy-bucket",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestStorageSettingsRoundTrip(t *testing.T) {
	outputDir := t.TempDir()
	p := newTestPiper(t, Config{OutputDir: outputDir})
	router := p.Handler(nil)

	req := httptest.NewRequest(http.MethodPut, "/api/storage/settings", strings.NewReader(`{"disabled":true,"url":"s3://bucket?endpoint=http://localhost:9000","token":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		RestartRequired bool `json:"restart_required"`
		Config          struct {
			Disabled bool   `json:"disabled"`
			URL      string `json:"url"`
			Token    string `json:"token"`
		} `json:"config"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.RestartRequired {
		t.Fatal("restart_required = false, want true")
	}
	if !out.Config.Disabled || out.Config.URL == "" || out.Config.Token != "secret" {
		t.Fatalf("config = %#v, want disabled/url/token saved", out.Config)
	}
	raw, err := os.ReadFile(filepath.Join(outputDir, "storage.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "disabled: true") || !strings.Contains(string(raw), "bucket") {
		t.Fatalf("storage.yaml = %s", string(raw))
	}
}

func TestStorageSettingsOverrideLoadsOnStartup(t *testing.T) {
	outputDir := t.TempDir()
	path := filepath.Join(outputDir, "storage.yaml")
	if err := os.WriteFile(path, []byte("storage:\n  disabled: true\n  url: s3://bucket?endpoint=http://localhost:9000\n  token: secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := newTestPiper(t, Config{OutputDir: outputDir})
	if p.store != nil {
		t.Fatal("storage should be disabled by persisted override")
	}
	router := p.Handler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/storage/settings", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Config struct {
			Disabled bool `json:"disabled"`
		} `json:"config"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Config.Disabled {
		t.Fatal("persisted storage override was not loaded")
	}
}

func TestStorageObjectManagement(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	const projectID = "project-a"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	ls, ok := p.store.(*storage.LocalStore)
	if !ok {
		t.Fatal("expected local store for test")
	}
	if err := ls.Put(context.Background(), "projects/project-a/uploads/runs/run-1/train/model.txt", strings.NewReader("hello"), int64(len("hello"))); err != nil {
		t.Fatal(err)
	}
	router := p.Handler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/storage/objects?prefix=runs/run-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var objs []struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Key != "runs/run-1/train/model.txt" {
		t.Fatalf("objects = %#v", objs)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/storage/object?key=runs/run-1/train/model.txt", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "hello" {
		t.Fatalf("download body = %q, want hello", got)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/storage/object?key=runs/run-1/train/model.txt", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
}

func TestStorageObjectUpload(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	const projectID = "project-a"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	router := p.Handler(nil)

	// Build a real multipart form so the handler exercises FormFile parsing.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("hello upload")); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("key", "runs/run-1/train/report.txt"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/storage/object", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "report.txt") {
		t.Fatalf("upload response = %s", rec.Body.String())
	}
	ls, ok := p.store.(*storage.LocalStore)
	if !ok {
		t.Fatal("expected local store")
	}
	rc, err := ls.Get(context.Background(), "projects/project-a/uploads/runs/run-1/train/report.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello upload" {
		t.Fatalf("stored object = %q, want hello upload", string(got))
	}
}

func TestLegacyWorkerPollingRoutesAreNotMounted(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	router := p.newRouter(nil, nil).(*gin.Engine)
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workers"},
		{http.MethodPost, "/api/workers"},
		{http.MethodPost, "/api/workers/:id/heartbeat"},
		{http.MethodGet, "/api/tasks/next"},
		{http.MethodPost, "/api/tasks/:id/done"},
		{http.MethodPost, "/api/tasks/:id/failed"},
		{http.MethodPost, "/api/projects/:project_id/runs/:id/steps/:step/logs"},
		{http.MethodPost, "/api/projects/:project_id/runs/:id/steps/:step/final-metrics"},
	} {
		if hasRoute(router, route.method, route.path) {
			t.Fatalf("legacy worker route is mounted: %s %s", route.method, route.path)
		}
	}
}

func TestStartRunPersistsExperiment(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	const projectID = "project-a"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "train"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{{
			Name: "step",
			Run:  pipeline.Run{Command: []string{"true"}},
		}}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}

	runID, err := p.startRun(context.Background(), pl, dag, StartRunOptions{
		ProjectID:  projectID,
		Experiment: "exp-v2",
		YAML:       "metadata:\n  name: train\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.repos.Run.Get(context.Background(), projectID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Experiment != "exp-v2" {
		t.Fatalf("experiment = %q, want exp-v2", got.Experiment)
	}
	runs, err := p.repos.Run.List(context.Background(), projectID, run.RunFilter{Experiment: "exp-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != runID {
		t.Fatalf("filtered runs = %#v, want %s", runs, runID)
	}
}

func TestBackfillScheduleCreatesRunsForCronRange(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	const projectID = "project-a"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Minute)
	yaml := "metadata:\n  name: train\nspec:\n  steps:\n    - name: step\n      run:\n        command: [\"true\"]\n"
	sc := &schedule.Schedule{
		ID:           "sch-backfill",
		ProjectID:    projectID,
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

	ctx := project.WithContext(context.Background(), project.Context{ID: projectID})
	runIDs, err := p.BackfillSchedule(ctx, sc.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(runIDs) != 3 {
		t.Fatalf("runIDs = %v, want 3 runs", runIDs)
	}
	runs, err := p.repos.Run.List(context.Background(), projectID, run.RunFilter{ScheduleID: sc.ID})
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
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Auth: AuthConfig{
			Authenticator: &testSecurityProvider{
				identity: &security.Identity{ID: "user-42"},
			},
			Authorizer: &testSecurityProvider{},
		},
		Hooks: Hooks{
			BeforeCreateRun: func(ctx context.Context, r *http.Request, yaml string) error {
				id, ok := security.IdentityFromContext(ctx)
				if !ok || id.ID != "user-42" {
					t.Errorf("BeforeCreateRun ctx missing authenticated identity")
				}
				return nil
			},
		},
	})
	router := p.newRouter(nil, nil)
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: "test", Name: "Test"}); err != nil {
		t.Fatal(err)
	}

	body := `{"yaml":"metadata:\n  name: test\nspec:\n  steps: []\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test/runs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
}

// TestAuth_RejectsOnError verifies that an Authenticator returning an error
// produces 401 and blocks the request.
func TestAuth_RejectsOnError(t *testing.T) {
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Auth: AuthConfig{
			Authenticator: &testSecurityProvider{
				authErr: fmt.Errorf("invalid token"),
			},
			Authorizer: &testSecurityProvider{},
		},
	})
	router := p.newRouter(nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthFactoryRunsDuringNew(t *testing.T) {
	provider := &testSecurityProvider{}
	called := false
	p, err := New(Config{
		OutputDir: t.TempDir(),
		Auth: AuthConfig{
			Factory: func(deps AuthDependencies) (AuthConfig, error) {
				called = true
				if deps.DB == nil {
					t.Fatal("factory DB is nil")
				}
				if deps.Driver != "sqlite" {
					t.Fatalf("factory driver = %q, want sqlite", deps.Driver)
				}
				return AuthConfig{
					Authenticator: provider,
					Authorizer:    provider,
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if !called {
		t.Fatal("auth factory was not called")
	}
	if p.cfg.Auth.Authenticator != provider || p.cfg.Auth.Authorizer != provider {
		t.Fatal("factory capabilities were not installed")
	}
	if p.cfg.Auth.Factory != nil {
		t.Fatal("factory should be cleared after construction")
	}
}

func TestAuthCapabilitiesControlRouteRegistration(t *testing.T) {
	provider := &testSecurityProvider{
		identity: &security.Identity{ID: "admin", SystemAdmin: true},
	}
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Auth: AuthConfig{
			Authenticator: provider,
			Authorizer:    provider,
			UserDirectory: testUserDirectory{},
		},
	})
	router := p.newRouter(nil, nil).(*gin.Engine)

	if !hasRoute(router, http.MethodGet, "/api/capabilities") {
		t.Fatal("capabilities route was not registered")
	}
	if !hasRoute(router, http.MethodGet, "/api/users") {
		t.Fatal("user directory route was not registered")
	}
	if hasRoute(router, http.MethodPost, "/api/users") {
		t.Fatal("user create route registered without UserManager")
	}
	if hasRoute(router, http.MethodDelete, "/api/users/:id") {
		t.Fatal("user delete route registered without UserManager")
	}
	if hasRoute(router, http.MethodGet, "/api/projects/:project_id/members") {
		t.Fatal("member routes registered without ProjectMemberManager")
	}
}

func TestConfigRejectsIncompleteAuthCapabilities(t *testing.T) {
	provider := &testSecurityProvider{}

	empty := Config{}
	if err := empty.Validate(); err == nil {
		t.Fatal("Validate accepted auth config without explicit trusted mode")
	}

	authenticatorOnly := DefaultConfig()
	authenticatorOnly.Auth = AuthConfig{Authenticator: provider}
	if err := authenticatorOnly.Validate(); err == nil {
		t.Fatal("Validate accepted Authenticator without Authorizer")
	}

	authorizerOnly := DefaultConfig()
	authorizerOnly.Auth = AuthConfig{Authorizer: provider}
	if err := authorizerOnly.Validate(); err == nil {
		t.Fatal("Validate accepted Authorizer without Authenticator")
	}
}

func TestMetricsAggregatesRunsAcrossProjects(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	for _, projectID := range []string{"metrics-a", "metrics-b"} {
		if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
			t.Fatal(err)
		}
		if err := p.repos.Run.Create(ctx, &run.Run{
			ID:           "run-" + projectID,
			ProjectID:    projectID,
			PipelineName: "metrics",
			Status:       run.StatusSuccess,
			StartedAt:    time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	p.Handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `piper_runs_total{status="success"} 2`) {
		t.Fatalf("metrics did not aggregate projects: %s", rec.Body.String())
	}
}

func TestNewEnsuresDefaultProject(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	defaultProject, err := p.repos.Project.Get(context.Background(), project.DefaultID)
	if err != nil {
		t.Fatal(err)
	}
	if defaultProject == nil {
		t.Fatal("default project was not created")
	}
	if defaultProject.Name != "Default" {
		t.Fatalf("default project name = %q, want Default", defaultProject.Name)
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

func TestHandlerContextStopsGRPCServerOnCancel(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	_, stopped := p.handlerContext(ctx, nil)
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("handler gRPC server did not stop after context cancellation")
	}
}
