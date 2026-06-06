package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandlerListAgents(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Kind: KindK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})
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
}
