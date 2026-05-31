package serving

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// stubServingRepo is an in-memory Repository for handler tests.
type stubServingRepo struct {
	services map[string]*Service
}

func newStubServingRepo(svcs ...*Service) *stubServingRepo {
	m := make(map[string]*Service, len(svcs))
	for _, s := range svcs {
		m[s.Name] = s
	}
	return &stubServingRepo{services: m}
}

func (r *stubServingRepo) Create(_ context.Context, svc *Service) error {
	r.services[svc.Name] = svc
	return nil
}
func (r *stubServingRepo) Get(_ context.Context, name string) (*Service, error) {
	return r.services[name], nil
}
func (r *stubServingRepo) Update(_ context.Context, svc *Service) error {
	r.services[svc.Name] = svc
	return nil
}
func (r *stubServingRepo) Upsert(_ context.Context, svc *Service) error {
	r.services[svc.Name] = svc
	return nil
}
func (r *stubServingRepo) SetStatus(_ context.Context, name, status string) error {
	if s, ok := r.services[name]; ok {
		s.Status = status
	}
	return nil
}
func (r *stubServingRepo) List(_ context.Context) ([]*Service, error) {
	out := make([]*Service, 0, len(r.services))
	for _, s := range r.services {
		out = append(out, s)
	}
	return out, nil
}
func (r *stubServingRepo) Delete(_ context.Context, name string) error {
	delete(r.services, name)
	return nil
}
func (r *stubServingRepo) ListHistory(_ context.Context) ([]*ServiceHistory, error) {
	return nil, nil
}

func newServingRouter(deps HandlerDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewHandler(deps).RegisterRoutes(r.Group(""))
	return r
}

func TestListServices(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "fraud-detector", Status: StatusRunning})
	router := newServingRouter(HandlerDeps{Services: repo})

	req := httptest.NewRequest(http.MethodGet, "/serving", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var svcs []Service
	if err := json.Unmarshal(rec.Body.Bytes(), &svcs); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(svcs) != 1 || svcs[0].Name != "fraud-detector" {
		t.Fatalf("unexpected services: %+v", svcs)
	}
}

func TestCreateService(t *testing.T) {
	repo := newStubServingRepo()
	deployed := false
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Deploy: func(_ context.Context, yamlBytes []byte, ownerID string) (*Service, error) {
			deployed = true
			svc := &Service{Name: "my-model", Status: StatusRunning}
			_ = repo.Upsert(context.Background(), svc)
			return svc, nil
		},
	})

	body := `{"yaml":"apiVersion: piper/v1\nkind: ModelService\n"}`
	req := httptest.NewRequest(http.MethodPost, "/serving", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !deployed {
		t.Fatal("Deploy was not called")
	}
}

func TestGetServiceNotFound(t *testing.T) {
	router := newServingRouter(HandlerDeps{Services: newStubServingRepo()})

	req := httptest.NewRequest(http.MethodGet, "/serving/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetServiceFound(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "model-v1", Status: StatusRunning, Endpoint: "http://localhost:8000"})
	router := newServingRouter(HandlerDeps{Services: repo})

	req := httptest.NewRequest(http.MethodGet, "/serving/model-v1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var svc Service
	if err := json.Unmarshal(rec.Body.Bytes(), &svc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if svc.Name != "model-v1" {
		t.Fatalf("Name = %q, want model-v1", svc.Name)
	}
}

func TestDeleteService(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "old-model", Status: StatusStopped})
	stopped := false
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Stop: func(_ context.Context, name string) error {
			stopped = true
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodDelete, "/serving/old-model", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if !stopped {
		t.Fatal("Stop was not called")
	}
	if _, ok := repo.services["old-model"]; ok {
		t.Fatal("service was not deleted from repo")
	}
}

func TestRestartService(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "live-model", Status: StatusRunning})
	restarted := ""
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Restart: func(_ context.Context, name string) error {
			restarted = name
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/serving/live-model/restart", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if restarted != "live-model" {
		t.Fatalf("Restart called with %q, want live-model", restarted)
	}
}

// ── hook tests ────────────────────────────────────────────────────────────────

type stubServingHooks struct {
	beforeCreate func(ctx context.Context, r *http.Request, yaml string) error
	beforeList   func(ctx context.Context, r *http.Request) (ServingFilter, error)
	beforeGet    func(ctx context.Context, r *http.Request, name string) error
}

func (h *stubServingHooks) BeforeCreateService(ctx context.Context, r *http.Request, yaml string) error {
	if h.beforeCreate != nil {
		return h.beforeCreate(ctx, r, yaml)
	}
	return nil
}
func (h *stubServingHooks) BeforeListServices(ctx context.Context, r *http.Request) (ServingFilter, error) {
	if h.beforeList != nil {
		return h.beforeList(ctx, r)
	}
	return ServingFilter{}, nil
}
func (h *stubServingHooks) BeforeGetService(ctx context.Context, r *http.Request, name string) error {
	if h.beforeGet != nil {
		return h.beforeGet(ctx, r, name)
	}
	return nil
}

func doServingJSON(router *gin.Engine, method, path string, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestBeforeCreateService_Blocks(t *testing.T) {
	router := newServingRouter(HandlerDeps{
		Services: newStubServingRepo(),
		Hooks: &stubServingHooks{
			beforeCreate: func(_ context.Context, _ *http.Request, _ string) error {
				return errors.New("not allowed")
			},
		},
	})

	rec := doServingJSON(router, http.MethodPost, "/serving", `{"yaml":"apiVersion: piper/v1"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body)
	}
}

func TestBeforeListServices_OwnerFilter(t *testing.T) {
	repo := newStubServingRepo(
		&Service{Name: "svc-a", OwnerID: "user-1"},
		&Service{Name: "svc-b", OwnerID: "user-2"},
	)
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Hooks: &stubServingHooks{
			beforeList: func(_ context.Context, _ *http.Request) (ServingFilter, error) {
				return ServingFilter{OwnerID: "user-1"}, nil
			},
		},
	})

	rec := doServingJSON(router, http.MethodGet, "/serving", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []Service
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 1 || out[0].Name != "svc-a" {
		t.Errorf("expected only svc-a (user-1), got %+v", out)
	}
}

func TestBeforeListServices_Error(t *testing.T) {
	router := newServingRouter(HandlerDeps{
		Services: newStubServingRepo(),
		Hooks: &stubServingHooks{
			beforeList: func(_ context.Context, _ *http.Request) (ServingFilter, error) {
				return ServingFilter{}, errors.New("auth error")
			},
		},
	})

	rec := doServingJSON(router, http.MethodGet, "/serving", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestBeforeGetService_BlocksGet(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "model-v1"})
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Hooks: &stubServingHooks{
			beforeGet: func(_ context.Context, _ *http.Request, _ string) error {
				return errors.New("forbidden")
			},
		},
	})

	rec := doServingJSON(router, http.MethodGet, "/serving/model-v1", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestBeforeGetService_BlocksDelete(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "model-v1"})
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Hooks: &stubServingHooks{
			beforeGet: func(_ context.Context, _ *http.Request, _ string) error {
				return errors.New("forbidden")
			},
		},
	})

	rec := doServingJSON(router, http.MethodDelete, "/serving/model-v1", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if _, ok := repo.services["model-v1"]; !ok {
		t.Error("service should not be deleted when hook blocks")
	}
}

func TestBeforeGetService_BlocksRestart(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "model-v1"})
	restartCalled := false
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Restart: func(_ context.Context, _ string) error {
			restartCalled = true
			return nil
		},
		Hooks: &stubServingHooks{
			beforeGet: func(_ context.Context, _ *http.Request, _ string) error {
				return errors.New("forbidden")
			},
		},
	})

	rec := doServingJSON(router, http.MethodPost, "/serving/model-v1/restart", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if restartCalled {
		t.Error("Restart should not be called when hook blocks")
	}
}

func TestBeforeGetService_PassesName(t *testing.T) {
	repo := newStubServingRepo(&Service{Name: "my-model"})
	gotName := ""
	router := newServingRouter(HandlerDeps{
		Services: repo,
		Hooks: &stubServingHooks{
			beforeGet: func(_ context.Context, _ *http.Request, name string) error {
				gotName = name
				return nil
			},
		},
	})

	doServingJSON(router, http.MethodGet, "/serving/my-model", "")
	if gotName != "my-model" {
		t.Errorf("BeforeGetService called with %q, want my-model", gotName)
	}
}
