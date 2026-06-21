package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
)

func TestHandlerListAgents(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Infrastructure: InfrastructureK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})
	r := gin.New()
	NewHandler(reg).RegisterRoutes(r.Group("/api"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"cluster_name":"gpu-a"`) {
		t.Fatalf("list body missing agent: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"infrastructure":"k8s"`) || strings.Contains(rec.Body.String(), `"kind":`) {
		t.Fatalf("list body uses inconsistent infrastructure field: %s", rec.Body.String())
	}
}

func TestHandlerHasSingleWorkerListEndpoint(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	NewHandler(NewRegistry()).RegisterRoutes(router.Group("/api"))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/notebook-workers", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("legacy notebook worker list status = %d, want 404", recorder.Code)
	}
}

// inMemoryPolicyRepo is a thread-safe in-memory WorkerPodPolicyRepository for tests.
type inMemoryPolicyRepo struct {
	mu       sync.RWMutex
	policies map[string]WorkerPodPolicy
}

func newInMemoryPolicyRepo() *inMemoryPolicyRepo {
	return &inMemoryPolicyRepo{policies: make(map[string]WorkerPodPolicy)}
}

func (r *inMemoryPolicyRepo) List(_ context.Context) ([]WorkerPodPolicy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]WorkerPodPolicy, 0, len(r.policies))
	for _, p := range r.policies {
		cp := p
		out = append(out, cp)
	}
	return out, nil
}

func (r *inMemoryPolicyRepo) Get(_ context.Context, workerID string) (*WorkerPodPolicy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.policies[workerID]
	if !ok {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (r *inMemoryPolicyRepo) Set(_ context.Context, p WorkerPodPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[p.WorkerID] = p
	return nil
}

func (r *inMemoryPolicyRepo) Delete(_ context.Context, workerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.policies, workerID)
	return nil
}

func setupPodPolicyRouter() (*gin.Engine, *inMemoryPolicyRepo) {
	gin.SetMode(gin.ReleaseMode)
	reg := NewRegistry()
	reg.Register(Info{
		ID:             "nb-worker-1",
		Infrastructure: InfrastructureK8s,
		ClusterName:    "gpu-cluster",
		Capabilities:   []string{CapabilityNotebook},
	})
	repo := newInMemoryPolicyRepo()
	r := gin.New()
	NewHandler(reg, repo).RegisterRoutes(r.Group("/api"))
	return r, repo
}

func TestHandlerGetPodPolicy_NotFound(t *testing.T) {
	r, _ := setupPodPolicyRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notebook-workers/nb-worker-1/pod-policy", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerSetAndGetPodPolicy(t *testing.T) {
	r, _ := setupPodPolicyRouter()

	body := `{"yaml": "spec:\n  nodeSelector:\n    nvidia.com/gpu: \"true\"\n"}`
	req := httptest.NewRequest(http.MethodPut, "/api/notebook-workers/nb-worker-1/pod-policy",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}

	// GET should return the saved policy
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/notebook-workers/nb-worker-1/pod-policy", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", rec2.Code, rec2.Body.String())
	}

	var policy WorkerPodPolicy
	if err := json.Unmarshal(rec2.Body.Bytes(), &policy); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if policy.WorkerID != "nb-worker-1" {
		t.Errorf("worker_id: want nb-worker-1, got %q", policy.WorkerID)
	}
	if policy.PodTemplate.Spec.NodeSelector["nvidia.com/gpu"] != "true" {
		t.Errorf("node selector not saved: %v", policy.PodTemplate.Spec.NodeSelector)
	}
}

func TestHandlerSetPodPolicy_InvalidYAML(t *testing.T) {
	r, _ := setupPodPolicyRouter()
	body := `{"yaml": "not: valid: yaml: ["}`
	req := httptest.NewRequest(http.MethodPut, "/api/notebook-workers/nb-worker-1/pod-policy",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid YAML, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerSetPodPolicy_EmptyYAML(t *testing.T) {
	r, _ := setupPodPolicyRouter()
	body := `{"yaml": ""}`
	req := httptest.NewRequest(http.MethodPut, "/api/notebook-workers/nb-worker-1/pod-policy",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty YAML, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeletePodPolicy(t *testing.T) {
	r, repo := setupPodPolicyRouter()

	// Pre-seed a policy
	_ = repo.Set(context.Background(), WorkerPodPolicy{
		WorkerID: "nb-worker-1",
		PodTemplate: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"gpu": "true"},
			},
		},
	})

	// DELETE
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/notebook-workers/nb-worker-1/pod-policy", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d body=%s", rec.Code, rec.Body.String())
	}

	// GET should return 404 now
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/notebook-workers/nb-worker-1/pod-policy", nil))
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rec2.Code)
	}
}

func TestHandlerPodPolicy_NilRepo(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	reg := NewRegistry()
	r := gin.New()
	// No policy repo wired
	NewHandler(reg).RegisterRoutes(r.Group("/api"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notebook-workers/x/pod-policy", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nil repo GET: expected 404, got %d", rec.Code)
	}

	body := `{"yaml": "spec: {}"}`
	req := httptest.NewRequest(http.MethodPut, "/api/notebook-workers/x/pod-policy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusInternalServerError {
		t.Fatalf("nil repo PUT: expected 500, got %d", rec2.Code)
	}
}
