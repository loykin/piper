package notebook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// stubNotebookRepo is an in-memory Repository for handler response tests.
type stubNotebookRepo struct {
	servers map[string]*NotebookServer
}

func newStubNotebookRepo(nbs ...*NotebookServer) *stubNotebookRepo {
	m := make(map[string]*NotebookServer, len(nbs))
	for _, nb := range nbs {
		m[nb.Name] = nb
	}
	return &stubNotebookRepo{servers: m}
}

func (r *stubNotebookRepo) Create(_ context.Context, nb *NotebookServer) error {
	r.servers[nb.Name] = nb
	return nil
}
func (r *stubNotebookRepo) Get(_ context.Context, _, name string) (*NotebookServer, error) {
	return r.servers[name], nil
}
func (r *stubNotebookRepo) GetByVolumeID(_ context.Context, _, _ string) (*NotebookServer, error) {
	return nil, nil
}
func (r *stubNotebookRepo) Update(_ context.Context, nb *NotebookServer) error {
	r.servers[nb.Name] = nb
	return nil
}
func (r *stubNotebookRepo) SetStatus(_ context.Context, _, name, status string) error {
	if nb, ok := r.servers[name]; ok {
		nb.Status = status
	}
	return nil
}
func (r *stubNotebookRepo) List(_ context.Context, _ string) ([]*NotebookServer, error) {
	out := make([]*NotebookServer, 0, len(r.servers))
	for _, nb := range r.servers {
		out = append(out, nb)
	}
	return out, nil
}
func (r *stubNotebookRepo) Delete(_ context.Context, _, name string) error {
	delete(r.servers, name)
	return nil
}
func (r *stubNotebookRepo) AppendHistory(_ context.Context, _ *NotebookServer) error {
	return nil
}
func (r *stubNotebookRepo) ListHistory(_ context.Context, _ string, _, _ int) ([]*NotebookHistory, error) {
	return nil, nil
}
func (r *stubNotebookRepo) CountHistory(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func injectNotebookProjectCtx(id string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: id, Role: security.ProjectRoleAdmin})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func newNotebookRouter(deps HandlerDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewHandler(deps).RegisterRoutes(r.Group("", injectNotebookProjectCtx("test-proj")))
	return r
}

// secretNotebookServer returns a fully populated NotebookServer whose Token
// and PID must never reach an API response.
func secretNotebookServer(name string) *NotebookServer {
	return &NotebookServer{
		ProjectID: "test-proj",
		Name:      name,
		Status:    StatusRunning,
		Env:       "python3",
		Endpoint:  "http://127.0.0.1:18888",
		PID:       424242,
		WorkDir:   "/data/notebooks/" + name,
		Token:     "super-secret-jupyter-token",
		RuntimeID: "rt-1",
		VolumeID:  "vol-1",
		Image:     "jupyter/base-notebook",
		YAML:      "apiVersion: piper/v1\nkind: Notebook\n",
		CreatedBy: "alice",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// assertNoSecretFields decodes the response body into a generic map (or
// slice of maps) and fails if a `token` or `pid` key is present anywhere,
// and if the raw body contains the literal secret token value. This is
// intentionally stricter than decoding into NotebookServerResponse, which
// would silently hide a leak by only reading fields it knows about.
func assertNoSecretFields(t *testing.T, body []byte) {
	t.Helper()
	if strings.Contains(string(body), "super-secret-jupyter-token") {
		t.Fatalf("response body leaks the Jupyter token: %s", body)
	}
	var generic any
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	walkAssertNoKeys(t, generic, "token", "pid")
}

func walkAssertNoKeys(t *testing.T, v any, forbidden ...string) {
	t.Helper()
	switch val := v.(type) {
	case map[string]any:
		for _, key := range forbidden {
			if _, ok := val[key]; ok {
				t.Fatalf("response object contains forbidden key %q: %+v", key, val)
			}
		}
		for _, nested := range val {
			walkAssertNoKeys(t, nested, forbidden...)
		}
	case []any:
		for _, nested := range val {
			walkAssertNoKeys(t, nested, forbidden...)
		}
	}
}

func TestListNotebooksExcludesTokenAndPID(t *testing.T) {
	repo := newStubNotebookRepo(secretNotebookServer("nb-1"))
	router := newNotebookRouter(HandlerDeps{Notebooks: repo})

	req := httptest.NewRequest(http.MethodGet, "/notebooks", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertNoSecretFields(t, rec.Body.Bytes())

	var got []NotebookServerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0].Name != "nb-1" {
		t.Fatalf("unexpected notebooks: %+v", got)
	}
	// Endpoint/WorkDir are the fields the UI still displays — confirm the
	// DTO didn't drop those along with Token/PID.
	if got[0].Endpoint == "" || got[0].WorkDir == "" {
		t.Fatalf("expected Endpoint/WorkDir to survive mapping: %+v", got[0])
	}
}

func TestGetNotebookExcludesTokenAndPID(t *testing.T) {
	repo := newStubNotebookRepo(secretNotebookServer("nb-1"))
	router := newNotebookRouter(HandlerDeps{Notebooks: repo})

	req := httptest.NewRequest(http.MethodGet, "/notebooks/nb-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertNoSecretFields(t, rec.Body.Bytes())
}

func TestCreateNotebookExcludesTokenAndPID(t *testing.T) {
	repo := newStubNotebookRepo()
	router := newNotebookRouter(HandlerDeps{
		Notebooks: repo,
		Create: func(_ context.Context, projectID string, _ Notebook, _ string) (*NotebookServer, error) {
			nb := secretNotebookServer("nb-new")
			nb.ProjectID = projectID
			_ = repo.Create(context.Background(), nb)
			return nb, nil
		},
	})

	yamlBody := "apiVersion: piper/v1\nkind: Notebook\nmetadata:\n  name: nb-new\nspec:\n  driver: {}\n"
	reqBody, err := json.Marshal(map[string]string{"yaml": yamlBody})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/notebooks", strings.NewReader(string(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	assertNoSecretFields(t, rec.Body.Bytes())
}

// TestNotebookServerResponseFieldSet pins the exact set of JSON keys the DTO
// emits, so a future field addition to NotebookServer must be a deliberate
// decision in NewNotebookServerResponse rather than an accidental leak
// through direct struct embedding.
func TestNotebookServerResponseFieldSet(t *testing.T) {
	resp := NewNotebookServerResponse(secretNotebookServer("nb-1"))
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"project_id": true, "name": true, "status": true, "env": true,
		"endpoint": true, "work_dir": true, "runtime_id": true,
		"volume_id": true, "image": true, "yaml": true, "created_by": true,
		"created_at": true, "updated_at": true,
	}
	for k := range m {
		if !want[k] {
			t.Errorf("unexpected key %q in NotebookServerResponse JSON", k)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("expected key %q missing from NotebookServerResponse JSON", k)
	}
	if _, ok := m["token"]; ok {
		t.Error("token key must never appear")
	}
	if _, ok := m["pid"]; ok {
		t.Error("pid key must never appear")
	}
}

func TestNewNotebookServerResponseNilSafe(t *testing.T) {
	if got := NewNotebookServerResponse(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
	if got := NewNotebookServerResponses(nil); got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil slice, got %+v", got)
	}
}
