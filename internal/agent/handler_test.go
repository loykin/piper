package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandlerRegisterListHeartbeat(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	reg := NewRegistry()
	r := gin.New()
	NewHandler(reg).RegisterRoutes(r.Group("/api"))

	req := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(`{
		"id":"a1",
		"kind":"k8s",
		"cluster_name":"gpu-a",
		"capabilities":["k8s","tunnel"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/agents/a1/heartbeat", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("heartbeat status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"cluster_name":"gpu-a"`) {
		t.Fatalf("list body missing agent: %s", rec.Body.String())
	}
}
