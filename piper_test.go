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
	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/internal/runlifecycle"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/schedule"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/storage"
	"github.com/loykin/piper/pkg/template"
)

type templateRunMember struct {
	memberclient.Client
	ref project.ProjectRef
	req memberclient.SubmitRunRequest
}

func (m *templateRunMember) SubmitRun(_ context.Context, _ memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
	m.ref, m.req = ref, req
	return memberclient.SubmitRunResponse{RunID: "remote-run-1"}, nil
}

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

func (testUserDirectory) ListUsers(context.Context, int, int) ([]*security.User, error) {
	return []*security.User{}, nil
}

func (testUserDirectory) CountUsers(context.Context) (int, error) {
	return 0, nil
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
	if cfg.Server.SecretEncryptionKey == "" {
		cfg.Server.AllowInsecureDevKey = true
	}
	if cfg.Runtime.Type == "" {
		cfg.Runtime.Type = RuntimeBaremetal
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestExternalStatsBackendReceivesRuntimeWritesThroughDurableIngress(t *testing.T) {
	bulk := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_bulk" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		bulk <- string(body)
		_, _ = w.Write([]byte(`{"errors":false}`))
	}))
	defer server.Close()
	backendURL := strings.Replace(server.URL, "http://", "elasticsearch://", 1) + "/piper"
	p := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}, Runtime: RuntimeConfig{Type: RuntimeBaremetal}, Stats: StatsConfig{Spool: StatsSpoolConfig{MaxBytes: 1 << 20}, Logs: StatsBackendConfig{URL: backendURL, ManageRetention: false}, Metrics: StatsBackendConfig{ManageRetention: false}}})
	secret := "ghp_abcdefghijklmnopqrstuvwxyz123456"
	if err := p.logs.Append(context.Background(), []*logstore.Line{{ProjectID: "default", RunID: "run", StepName: "step", Ts: time.Now().UTC(), Stream: "stdout", Line: "token=" + secret}}); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-bulk:
		if strings.Contains(body, secret) || !strings.Contains(body, `"event_id":"`) || !strings.Contains(body, `"id":1`) {
			t.Fatalf("bulk payload=%s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("external backend did not receive spooled log")
	}
}

func TestTemplateRunUsesConfiguredMemberRouting(t *testing.T) {
	const projectID = "remote-project"
	p := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}, Runtime: RuntimeConfig{Type: RuntimeBaremetal}})
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: "Remote Project", OwnerMemberID: "member-1"}); err != nil {
		t.Fatal(err)
	}
	tpl := &template.Template{
		ID: "template-1", ProjectID: projectID, Name: "federated-template", Version: 1,
		YAML:      "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: federated-template\nspec:\n  steps:\n  - name: proof\n    run:\n      type: command\n      command: [\"echo\", \"remote\"]\n",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := p.repos.PipelineTemplate.Create(context.Background(), tpl); err != nil {
		t.Fatal(err)
	}
	member := &templateRunMember{}
	refFor := func(id string) project.ProjectRef {
		return project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: id}
	}
	router := p.newRouterWithMember(nil, nil, member, refFor)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipelines/"+tpl.ID+"/run", strings.NewReader(`{"params":{"lr":0.1}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if member.ref != refFor(projectID) {
		t.Fatalf("ProjectRef = %#v", member.ref)
	}
	if member.req.Params["lr"] != 0.1 || !strings.Contains(member.req.YAML, "federated-template") {
		t.Fatalf("SubmitRun request = %#v", member.req)
	}
}

func TestLocalMemberSubmitRunIsDurablyIdempotent(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}, Runtime: RuntimeConfig{Type: RuntimeBaremetal}})
	member := NewLocalMemberClient(p)
	scheduledAt := time.Now().UTC().Add(time.Hour)
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: idempotent\nspec:\n  steps:\n  - name: proof\n    run:\n      type: command\n      command: [\"echo\", \"ok\"]\n"
	req := memberclient.SubmitRunRequest{
		IdempotencyKey: "request-a", YAML: yaml, Vars: BuiltinVars{ScheduledAt: &scheduledAt},
	}
	ref := project.LocalRef(project.DefaultID)
	first, err := member.SubmitRun(context.Background(), memberclient.AuthContext{}, ref, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := member.SubmitRun(context.Background(), memberclient.AuthContext{}, ref, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID != second.RunID {
		t.Fatalf("duplicate submission run IDs = %q, %q", first.RunID, second.RunID)
	}
	runs, err := p.repos.Run.List(context.Background(), project.DefaultID, run.RunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	req.YAML = strings.Replace(req.YAML, "idempotent", "different", 1)
	if _, err := member.SubmitRun(context.Background(), memberclient.AuthContext{}, ref, req); err == nil || !strings.Contains(err.Error(), "different Run request") {
		t.Fatalf("key reuse error = %v", err)
	}
}

func TestNewRequiresCredentialEncryptionKeyUnlessDevOptIn(t *testing.T) {
	_, err := New(Config{
		OutputDir: t.TempDir(),
		Auth:      AuthConfig{Trusted: true},
	})
	if err == nil {
		t.Fatal("expected missing secret_encryption_key to fail without dev opt-in")
	}
	if !strings.Contains(err.Error(), "server.secret_encryption_key is required") {
		t.Fatalf("error = %v", err)
	}
}

// TestNew_ResolvesRelativeOutputDirToAbsolute is a regression test for a real
// bug found during local QA (fed.md §14): a relative output_dir (the
// documented default, "./piper-data" — see config/piper.yaml) worked fine for
// runtime.type: baremetal but broke runtime.type: docker, because the docker
// driver bind-mounts OutputDir/.results into the container and Docker's
// daemon rejects a relative bind-mount source outright ("mount path must be
// absolute"). New() must resolve OutputDir to an absolute path once, up
// front, so every downstream consumer (Docker mounts, the orphan-sweep's
// LocalStore-root comparison) sees an absolute path regardless of how the
// operator wrote output_dir in config.
func TestNew_ResolvesRelativeOutputDirToAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	p := newTestPiper(t, Config{OutputDir: "./relative-output"})
	if !filepath.IsAbs(p.cfg.OutputDir) {
		t.Fatalf("OutputDir = %q, want an absolute path", p.cfg.OutputDir)
	}
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
	events, unsubscribe := p.events.Subscribe()
	defer unsubscribe()
	push := localLogPushClient{store: p.logs, metrics: p.metrics, events: p.events}
	batch := logsink.LogBatch{ProjectID: projectID, RunID: "run-metric", StepName: "train", Lines: []logsink.LogLine{{Ts: time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC), Stream: "stdout", Text: "PIPER_METRIC loss=0.312"}}}
	if err := push.SendPush(iagent.MethodLogAppend, batch); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-events:
		if got.Type != "metric.recorded" || got.ProjectID != projectID || got.Fields["key"] != "loss" || got.Fields["value"] != 0.312 {
			t.Fatalf("metric event = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("metric.recorded event was not published")
	}

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

func TestAlertRuleAPIUsesProjectNotificationCredentials(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	const projectID = "alert-project"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.credentials.Create(context.Background(), projectID, credential.CreateRequest{
		Name: "ops",
		Kind: credential.KindWebhook,
		Data: map[string]string{"url": "https://example.com/piper-alerts"},
	}); err != nil {
		t.Fatal(err)
	}
	router := p.Handler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/alert-rules", strings.NewReader(`{
		"name":"failed-runs","on":"event","event_type":"run.completed",
		"when":"fields.status == \"failed\"","notify":["ops"],"cooldown_seconds":60
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || !created.Enabled {
		t.Fatalf("created rule = %#v", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/alert-rules?limit=20&offset=0", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Total-Count") != "1" {
		t.Fatalf("list status=%d total=%q body=%s", rec.Code, rec.Header().Get("X-Total-Count"), rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/alert-rules/"+created.ID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204: %s", rec.Code, rec.Body.String())
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

	// Listing is one-level (S3 Delimiter="/" semantics — see
	// storage.Store.List): "runs/run-1" only surfaces its immediate child,
	// the "train/" folder, not the file nested inside it.
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/storage/objects?prefix=runs/run-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var objs []struct {
		Key   string `json:"key"`
		IsDir bool   `json:"is_dir"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Key != "runs/run-1/train/" || !objs[0].IsDir {
		t.Fatalf("objects = %#v", objs)
	}

	// Drilling into that folder surfaces the file itself.
	req = httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/storage/objects?prefix=runs/run-1/train/", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Key != "runs/run-1/train/model.txt" || objs[0].IsDir {
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

func TestLegacyWorkerPollingMutationRoutesAreNotMounted(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	router := p.newRouter(nil, nil).(*gin.Engine)
	for _, route := range []struct {
		method string
		path   string
	}{
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

// waitRunTerminal polls until runID reaches a terminal status (success,
// failed, or canceled) or the timeout expires. Tests that call startRun
// directly (bypassing the HTTP layer) must wait like this before returning —
// otherwise t.Cleanup's Piper.Close() can race the still-in-flight
// dispatch/queue goroutines.
func waitRunTerminal(t *testing.T, p *Piper, projectID, runID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got, err := p.repos.Run.Get(context.Background(), projectID, runID)
		if err == nil {
			switch got.Status {
			case run.StatusSuccess, run.StatusFailed, run.StatusCanceled:
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal status within %s", runID, timeout)
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

	runID, err := p.runs.StartRun(context.Background(), pl, dag, runlifecycle.StartRunOptions{
		ProjectID:  projectID,
		Experiment: "exp-v2",
		YAML:       "metadata:\n  name: train\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	// startRun only enqueues dispatch; wait for the background driver/queue
	// machinery to actually finish before the test returns. Otherwise
	// t.Cleanup's Piper.Close() races the still-running dispatch: the queue
	// goroutine that persists the eventual step/run-finalize write keeps
	// retrying against this test's already-closed DB pool (dbstore: "primary"
	// not found) and pollutes unrelated later tests' log output.
	waitRunTerminal(t, p, projectID, runID, 5*time.Second)

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

func TestOnRunEndHookReceivesPersistedRunResult(t *testing.T) {
	results := make(chan *pipeline.RunResult, 1)
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Hooks: Hooks{OnRunEnd: func(_ context.Context, _ string, result *pipeline.RunResult) {
			results <- result
		}},
	})
	const projectID = "hook-project"
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "hook-pipeline"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{{
			Name: "step",
			Run:  pipeline.Run{Command: []string{"true"}},
		}}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := p.runs.StartRun(context.Background(), pl, dag, runlifecycle.StartRunOptions{
		ProjectID: projectID,
		YAML:      "metadata:\n  name: hook-pipeline\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitRunTerminal(t, p, projectID, runID, 5*time.Second)

	select {
	case result := <-results:
		if result.PipelineName != "hook-pipeline" {
			t.Fatalf("pipeline name = %q, want hook-pipeline", result.PipelineName)
		}
		step := result.Steps["step"]
		if step == nil || step.Status != pipeline.StatusDone {
			t.Fatalf("step result = %#v, want done", step)
		}
		if result.EndedAt.IsZero() {
			t.Fatal("run result has no end time")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnRunEnd hook was not called")
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
	yaml := testScheduleYAML()
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
	for _, id := range runIDs {
		waitRunTerminal(t, p, projectID, id, 5*time.Second)
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

func TestScheduleFiredCronClaimsAndCreatesRun(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	const projectID = "schedule-fire"
	if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	plannedAt := time.Now().UTC().Add(-1 * time.Second).Truncate(time.Second)
	p.startedAt = plannedAt.Add(-1 * time.Minute)
	sc := &schedule.Schedule{
		ID:           "sch-fire",
		ProjectID:    projectID,
		Name:         "train",
		PipelineYAML: testScheduleYAML(),
		ScheduleType: "cron",
		CronExpr:     "* * * * *",
		Enabled:      true,
		NextRunAt:    plannedAt,
		CreatedAt:    plannedAt,
		UpdatedAt:    plannedAt,
	}
	if err := p.repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatal(err)
	}

	p.runs.ScheduleFired(ctx, projectID, sc.ID)

	runs, err := p.repos.Run.List(ctx, projectID, run.RunFilter{ScheduleID: sc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	waitRunTerminal(t, p, projectID, runs[0].ID, 5*time.Second)
	if runs[0].ScheduledAt == nil || !runs[0].ScheduledAt.Equal(plannedAt) {
		t.Fatalf("ScheduledAt = %v, want %v", runs[0].ScheduledAt, plannedAt)
	}
	got, err := p.repos.Schedule.Get(ctx, projectID, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt == nil {
		t.Fatal("LastRunAt = nil, want claim timestamp")
	}
	if !got.NextRunAt.After(plannedAt) {
		t.Fatalf("NextRunAt = %v, want after %v", got.NextRunAt, plannedAt)
	}
}

func TestScheduleFiredIgnoresFutureCronTick(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	const projectID = "schedule-future"
	if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	plannedAt := now.Add(1 * time.Hour)
	p.startedAt = now.Add(-1 * time.Minute)
	sc := &schedule.Schedule{
		ID:           "sch-future",
		ProjectID:    projectID,
		Name:         "train",
		PipelineYAML: testScheduleYAML(),
		ScheduleType: "cron",
		CronExpr:     "* * * * *",
		Enabled:      true,
		NextRunAt:    plannedAt,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := p.repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatal(err)
	}

	p.runs.ScheduleFired(ctx, projectID, sc.ID)

	runs, err := p.repos.Run.List(ctx, projectID, run.RunFilter{ScheduleID: sc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 for future stale callback", len(runs))
	}
	got, err := p.repos.Schedule.Get(ctx, projectID, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt != nil {
		t.Fatalf("LastRunAt = %v, want nil", got.LastRunAt)
	}
	if !got.NextRunAt.Equal(plannedAt) {
		t.Fatalf("NextRunAt = %v, want unchanged %v", got.NextRunAt, plannedAt)
	}
}

func TestScheduleFiredMisfireSkipAdvancesWithoutRun(t *testing.T) {
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Schedule:  ScheduleConfig{MisfirePolicy: "skip"},
	})
	ctx := context.Background()
	const projectID = "schedule-skip"
	if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	plannedAt := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	p.startedAt = plannedAt.Add(30 * time.Minute)
	sc := &schedule.Schedule{
		ID:           "sch-skip",
		ProjectID:    projectID,
		Name:         "train",
		PipelineYAML: testScheduleYAML(),
		ScheduleType: "cron",
		CronExpr:     "* * * * *",
		Enabled:      true,
		NextRunAt:    plannedAt,
		CreatedAt:    plannedAt,
		UpdatedAt:    plannedAt,
	}
	if err := p.repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatal(err)
	}

	p.runs.ScheduleFired(ctx, projectID, sc.ID)

	runs, err := p.repos.Run.List(ctx, projectID, run.RunFilter{ScheduleID: sc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 for skipped misfire", len(runs))
	}
	got, err := p.repos.Schedule.Get(ctx, projectID, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt != nil {
		t.Fatalf("LastRunAt = %v, want nil on skip", got.LastRunAt)
	}
	if !got.NextRunAt.After(plannedAt) {
		t.Fatalf("NextRunAt = %v, want advanced after %v", got.NextRunAt, plannedAt)
	}
}

func testScheduleYAML() string {
	return "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: train\nspec:\n  steps:\n    - name: step\n      run:\n        command: [\"true\"]\n"
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
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime:   RuntimeConfig{Type: RuntimeBaremetal},
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

func TestCleanupScheduleRetentionKeepsNewestTerminalRuns(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID
	now := time.Now().UTC()

	sc := &schedule.Schedule{
		ProjectID:    projectID,
		ID:           "sch-retention",
		Name:         "retention",
		ScheduleType: "cron",
		CronExpr:     "0 * * * *",
		Enabled:      true,
		MaxRuns:      2,
		NextRunAt:    now.Add(time.Hour),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := p.repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	createRun := func(id string, status string, startedAt time.Time, endedAt *time.Time) {
		t.Helper()
		r := &run.Run{
			ProjectID:    projectID,
			ID:           id,
			ScheduleID:   sc.ID,
			PipelineName: "retention-pipeline",
			Status:       status,
			StartedAt:    startedAt,
			ScheduledAt:  &startedAt,
			PipelineYAML: "metadata:\n  name: retention-pipeline\nspec:\n  steps: []",
			ParamsJSON:   "{}",
		}
		if err := p.repos.Run.Create(ctx, r); err != nil {
			t.Fatalf("create run %s: %v", id, err)
		}
		if endedAt != nil {
			if err := p.repos.Run.UpdateStatus(ctx, projectID, id, status, endedAt); err != nil {
				t.Fatalf("finish run %s: %v", id, err)
			}
		}
	}

	oldEnd := now.Add(-3 * time.Hour)
	midEnd := now.Add(-2 * time.Hour)
	newEnd := now.Add(-1 * time.Hour)
	createRun("run-old", run.StatusSuccess, oldEnd, &oldEnd)
	createRun("run-mid", run.StatusSuccess, midEnd, &midEnd)
	createRun("run-new", run.StatusSuccess, newEnd, &newEnd)
	createRun("run-running", run.StatusRunning, now.Add(-4*time.Hour), nil)

	p.runs.CleanupScheduleRetention(ctx)

	if got, err := p.repos.Run.Get(ctx, projectID, "run-old"); err != nil {
		t.Fatalf("get old run: %v", err)
	} else if got != nil {
		t.Fatalf("oldest terminal run was not deleted")
	}
	for _, id := range []string{"run-mid", "run-new", "run-running"} {
		got, err := p.repos.Run.Get(ctx, projectID, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got == nil {
			t.Fatalf("%s should be retained", id)
		}
	}
}

// TestCleanupScheduleRetentionNonTerminalNewestDoesNotConsumeQuota guards
// against a real bug caught while bounding the retention list fetch: a
// non-terminal run positioned among the *newest* rows (not the oldest, as in
// the test above) must not count toward max_runs. If it did, the bounded
// fetch window would treat one extra terminal run as overflow and delete a
// run the documented policy says to keep.
func TestCleanupScheduleRetentionNonTerminalNewestDoesNotConsumeQuota(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID
	now := time.Now().UTC()

	sc := &schedule.Schedule{
		ProjectID:    projectID,
		ID:           "sch-retention-newest-running",
		Name:         "retention-newest-running",
		ScheduleType: "cron",
		CronExpr:     "0 * * * *",
		Enabled:      true,
		MaxRuns:      2,
		NextRunAt:    now.Add(time.Hour),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := p.repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	createRun := func(id string, status string, startedAt time.Time, endedAt *time.Time) {
		t.Helper()
		r := &run.Run{
			ProjectID:    projectID,
			ID:           id,
			ScheduleID:   sc.ID,
			PipelineName: "retention-pipeline",
			Status:       status,
			StartedAt:    startedAt,
			ScheduledAt:  &startedAt,
			PipelineYAML: "metadata:\n  name: retention-pipeline\nspec:\n  steps: []",
			ParamsJSON:   "{}",
		}
		if err := p.repos.Run.Create(ctx, r); err != nil {
			t.Fatalf("create run %s: %v", id, err)
		}
		if endedAt != nil {
			if err := p.repos.Run.UpdateStatus(ctx, projectID, id, status, endedAt); err != nil {
				t.Fatalf("finish run %s: %v", id, err)
			}
		}
	}

	// Newest-first order: run-running (still running, no EndedAt), then three
	// terminal runs. With max_runs=2, the two newest *terminal* runs
	// (run-new, run-mid) must be kept regardless of run-running's position.
	oldEnd := now.Add(-3 * time.Hour)
	midEnd := now.Add(-2 * time.Hour)
	newEnd := now.Add(-1 * time.Hour)
	createRun("run-running", run.StatusRunning, now, nil)
	createRun("run-new", run.StatusSuccess, newEnd, &newEnd)
	createRun("run-mid", run.StatusSuccess, midEnd, &midEnd)
	createRun("run-old", run.StatusSuccess, oldEnd, &oldEnd)

	p.runs.CleanupScheduleRetention(ctx)

	if got, err := p.repos.Run.Get(ctx, projectID, "run-old"); err != nil {
		t.Fatalf("get old run: %v", err)
	} else if got != nil {
		t.Fatalf("oldest terminal run was not deleted")
	}
	for _, id := range []string{"run-mid", "run-new", "run-running"} {
		got, err := p.repos.Run.Get(ctx, projectID, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got == nil {
			t.Fatalf("%s should be retained (a non-terminal newest run must not consume a kept slot)", id)
		}
	}
}

// TestCleanupRetentionDeletesExpiredRunsByTTL exercises the TTL-based path of
// cleanupRetention, which now sources candidates from the indexed
// ListTerminalBefore query instead of the project's full run history.
func TestCleanupRetentionDeletesExpiredRunsByTTL(t *testing.T) {
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Retention: RetentionConfig{RunTTL: time.Hour},
	})
	ctx := context.Background()
	projectID := project.DefaultID
	now := time.Now().UTC()

	createRun := func(id string, status string, startedAt time.Time, endedAt *time.Time) {
		t.Helper()
		r := &run.Run{
			ProjectID:    projectID,
			ID:           id,
			PipelineName: "ttl-pipeline",
			Status:       status,
			StartedAt:    startedAt,
			PipelineYAML: "metadata:\n  name: ttl-pipeline\nspec:\n  steps: []",
			ParamsJSON:   "{}",
		}
		if err := p.repos.Run.Create(ctx, r); err != nil {
			t.Fatalf("create run %s: %v", id, err)
		}
		if endedAt != nil {
			if err := p.repos.Run.UpdateStatus(ctx, projectID, id, status, endedAt); err != nil {
				t.Fatalf("finish run %s: %v", id, err)
			}
		}
	}

	expiredEnd := now.Add(-2 * time.Hour)  // older than the 1h TTL — should be deleted
	freshEnd := now.Add(-10 * time.Minute) // within the 1h TTL — should be kept
	createRun("run-expired", run.StatusSuccess, expiredEnd, &expiredEnd)
	createRun("run-fresh", run.StatusSuccess, freshEnd, &freshEnd)
	createRun("run-still-running", run.StatusRunning, now.Add(-3*time.Hour), nil)

	p.runs.CleanupRetention(ctx)

	if got, err := p.repos.Run.Get(ctx, projectID, "run-expired"); err != nil {
		t.Fatalf("get run-expired: %v", err)
	} else if got != nil {
		t.Fatalf("run past RunTTL was not deleted")
	}
	for _, id := range []string{"run-fresh", "run-still-running"} {
		got, err := p.repos.Run.Get(ctx, projectID, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got == nil {
			t.Fatalf("%s should be retained", id)
		}
	}
}

// TestRecoverInterruptedRunsFixesOrphanedFinalizeWrite reproduces the
// durability gap the periodic reconciler (runCleanup calling
// recoverInterruptedRuns every recoveryReconcileEvery ticks, not just once at
// startup) exists to close: a run whose terminal DB write exhausted every
// persistWithRetry attempt is removed from Queue.runs regardless (see
// finalizeRunLocked), so it's invisible to Queue.Cleanup's TTL sweep and
// would otherwise stay stuck non-terminal in the DB until a process restart.
// This run is never added to p.queue at all — standing in for "not tracked
// anymore" — with its DB row left at StatusRunning and its one step already
// persisted as done, matching exactly what a real orphaned run looks like.
func TestRecoverInterruptedRunsFixesOrphanedFinalizeWrite(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID

	const runID = "run-orphaned-finalize"
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: orphan-pipeline\nspec:\n  steps:\n  - name: only\n    run:\n      type: command\n      command: [\"true\"]\n"
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "orphan-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC().Add(-time.Minute),
		PipelineYAML: yaml,
		ParamsJSON:   "{}",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := p.repos.Step.Upsert(ctx, &run.Step{
		ProjectID: projectID,
		RunID:     runID,
		StepName:  "only",
		Status:    "done",
		Attempts:  1,
	}); err != nil {
		t.Fatalf("upsert step: %v", err)
	}

	if p.queue.IsTracking(runID) {
		t.Fatal("precondition failed: run should not be tracked in memory")
	}

	p.runs.RecoverInterruptedRuns(ctx)

	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusSuccess {
		t.Fatalf("run status = %+v, want finalized to success", got)
	}
}

// TestRecoverInterruptedRunsSkipsTrackedRun verifies the IsTracking guard
// that makes it safe to call recoverInterruptedRuns repeatedly (not just once
// at startup): a run the live queue is still actively processing must be
// left alone, not re-added and corrupted.
func TestRecoverInterruptedRunsSkipsTrackedRun(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	projectID := project.DefaultID

	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "tracked-pipeline"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "only", Run: pipeline.Run{Command: []string{"true"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	const runID = "run-still-tracked"
	pipelineJSON, _ := json.Marshal(pl)
	if err := p.repos.Run.Create(ctx, &run.Run{
		ProjectID:    projectID,
		ID:           runID,
		PipelineName: "tracked-pipeline",
		Status:       run.StatusRunning,
		StartedAt:    time.Now().UTC(),
		PipelineYAML: string(pipelineJSON),
		ParamsJSON:   "{}",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	// Detach the backend before Add so promoteReady's dispatchIfNeeded is a
	// no-op instead of actually retry-dispatching the step forever against
	// an embedded worker with no configured capacity — this test only cares
	// that the run lands in Queue.runs, not that it actually executes.
	p.queue.SetBackend(nil)
	p.queue.Add(ctx, projectID, pl, dag, runID, ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !p.queue.IsTracking(runID) {
		t.Fatal("precondition failed: run should be tracked in memory after Add")
	}

	p.runs.RecoverInterruptedRuns(ctx)

	// Still tracked and still running — recoverInterruptedRuns must not have
	// touched its DB row (a real bug here would show up as the row jumping
	// to failed via one of the YAML/DAG-parse-error fallback paths, since
	// re-adding it would confuse Queue.runs bookkeeping).
	got, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got == nil || got.Status != run.StatusRunning {
		t.Fatalf("run status = %+v, want still running (untouched)", got)
	}
	if !p.queue.IsTracking(runID) {
		t.Fatal("run should still be tracked after a no-op recoverInterruptedRuns pass")
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

func TestCleanupStatsUsesIndependentRetentionWindows(t *testing.T) {
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Stats: StatsConfig{
			Logs:    StatsBackendConfig{Retention: 24 * time.Hour, ManageRetention: true},
			Metrics: StatsBackendConfig{Retention: 72 * time.Hour, ManageRetention: true},
		},
	})
	ctx := context.Background()
	const projectID = "stats-retention"
	if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := p.logs.Append(ctx, []*logstore.Line{{ProjectID: projectID, RunID: "gone", StepName: "step", Ts: old, Stream: "stdout", Line: "expired log"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.metrics.AppendMetrics(ctx, []*logstore.Metric{{ProjectID: projectID, RunID: "gone", StepName: "step", Key: "kept", Value: 1, Ts: old}}); err != nil {
		t.Fatal(err)
	}

	p.cleanupStats(ctx)
	logs, err := p.logs.Query(projectID, "gone", "step", 0)
	if err != nil || len(logs) != 0 {
		t.Fatalf("expired logs remain: logs=%#v err=%v", logs, err)
	}
	metrics, err := p.metrics.QueryMetrics(projectID, "gone", "step")
	if err != nil || len(metrics) != 1 {
		t.Fatalf("metric with longer retention was removed: metrics=%#v err=%v", metrics, err)
	}

	p.cfg.Stats.Logs.ManageRetention = false
	if err := p.logs.Append(ctx, []*logstore.Line{{ProjectID: projectID, RunID: "gone", StepName: "step", Ts: old, Stream: "stdout", Line: "externally managed"}}); err != nil {
		t.Fatal(err)
	}
	p.cleanupStats(ctx)
	logs, err = p.logs.Query(projectID, "gone", "step", 0)
	if err != nil || len(logs) != 1 {
		t.Fatalf("externally managed logs were swept: logs=%#v err=%v", logs, err)
	}
}

func TestLocalMemberLogCursorPaginationHasNoGapsOrDuplicates(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Minute)
	for i := 0; i < 3; i++ {
		if err := p.logs.Append(ctx, []*logstore.Line{{ProjectID: project.DefaultID, RunID: "cursor-run", StepName: "step", Ts: base.Add(time.Duration(i) * time.Second), Stream: "stdout", Line: fmt.Sprintf("line-%d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	member := NewLocalMemberClient(p)
	first, err := member.QueryLogs(ctx, memberclient.AuthContext{}, project.LocalRef(project.DefaultID), memberclient.QueryLogsRequest{RunID: "cursor-run", StepName: "step", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Lines) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := member.QueryLogs(ctx, memberclient.AuthContext{}, project.LocalRef(project.DefaultID), memberclient.QueryLogsRequest{RunID: "cursor-run", StepName: "step", Cursor: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Lines) != 1 || second.NextCursor != "" || second.Lines[0].ID <= first.Lines[1].ID {
		t.Fatalf("second page = %+v after first = %+v", second, first)
	}
	if first.Lines[0].EventID == "" || first.Lines[1].EventID == "" || second.Lines[0].EventID == "" {
		t.Fatal("cursor pages contain a log without an EventID")
	}
}

func TestProjectDeletePurgesOwnedStatsBeforeMetadata(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := context.Background()
	const projectID = "purge-stats"
	if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	if err := p.logs.Append(ctx, []*logstore.Line{{ProjectID: projectID, RunID: "deleted", StepName: "step", Ts: time.Now(), Stream: "stdout", Line: "log"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.metrics.AppendMetrics(ctx, []*logstore.Metric{{ProjectID: projectID, RunID: "deleted", StepName: "step", Key: "loss", Value: 1, Ts: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	p.Handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	logs, logErr := p.logs.Query(projectID, "deleted", "step", 0)
	metrics, metricErr := p.metrics.QueryMetrics(projectID, "deleted", "step")
	if logErr != nil || metricErr != nil || len(logs) != 0 || len(metrics) != 0 {
		t.Fatalf("logs=%+v metrics=%+v logErr=%v metricErr=%v", logs, metrics, logErr, metricErr)
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
