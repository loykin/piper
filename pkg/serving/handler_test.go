package serving

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
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
func (r *stubServingRepo) Get(_ context.Context, _, name string) (*Service, error) {
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
func (r *stubServingRepo) SetStatus(_ context.Context, _, name, status string) error {
	if s, ok := r.services[name]; ok {
		s.Status = status
	}
	return nil
}
func (r *stubServingRepo) SetStatusEndpoint(_ context.Context, _, name, status, endpoint string) error {
	if s, ok := r.services[name]; ok {
		s.Status = status
		if endpoint != "" {
			s.Endpoint = endpoint
		}
	}
	return nil
}
func (r *stubServingRepo) List(_ context.Context, _ string) ([]*Service, error) {
	out := make([]*Service, 0, len(r.services))
	for _, s := range r.services {
		out = append(out, s)
	}
	return out, nil
}
func (r *stubServingRepo) ListByWorker(_ context.Context, _ string) ([]*Service, error) {
	return r.List(context.Background(), "")
}
func (r *stubServingRepo) Delete(_ context.Context, _, name string) error {
	delete(r.services, name)
	return nil
}
func (r *stubServingRepo) ListHistory(_ context.Context, _ string) ([]*ServiceHistory, error) {
	return nil, nil
}

func injectProjectCtx(id string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: id, Role: security.ProjectRoleAdmin})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func newServingRouter(deps HandlerDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewHandler(deps).RegisterRoutes(r.Group("", injectProjectCtx("test-proj")))
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
		Deploy: func(_ context.Context, _ string, yamlBytes []byte) (*Service, error) {
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
		Stop: func(_ context.Context, _, name string) error {
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
		Restart: func(_ context.Context, _, name string) error {
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
