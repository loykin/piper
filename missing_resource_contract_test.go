package piper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/viewer"
)

// Routes whose GET legitimately answers something other than 404 for an
// unknown path parameter.
var missingResourceGETExceptions = map[string]string{
	"/api/projects/:project_id/runs/:id/mlflow-links":           "overlay keyed by run id; no links is an empty list",
	"/api/projects/:project_id/notebooks/:name/kernel-sessions": "overlay keyed by notebook name; no sessions is an empty list",
	"/api/projects/:project_id/notebooks/:name/documents":       "validates the ?path= query (400) before the lookup",
	"/custom/*path": "user-registered custom routes",
	"/projects/:project_id/services/predict/*path": "proxy validates the service name first (400)",
	"/ui/*filepath": "embedded UI is not built into test binaries",
}

// TestMissingResourcesAreNotFound walks every route that takes a path
// parameter, with ids that don't exist. None may answer 5xx — a lookup
// failure must be 404, not an internal error — and a GET must answer 404,
// not 200 with an empty body: the UI shows "Not Found" only for a 404 and
// "Failed to Load" for everything else, so both mistakes mislead the user.
func TestMissingResourcesAreNotFound(t *testing.T) {
	const projectID = "contract-project"
	p := newTestPiper(t, Config{OutputDir: t.TempDir(), Runtime: RuntimeConfig{Type: RuntimeBaremetal}})
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: "Contract"}); err != nil {
		t.Fatal(err)
	}
	engine := p.newRouterWithFederation(nil, viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir), nil, nil, nil, nil, "home-test").(*gin.Engine)
	checked := 0
	for _, route := range engine.Routes() {
		if !strings.ContainsAny(strings.ReplaceAll(route.Path, ":project_id", ""), ":*") {
			continue
		}
		// SSE streams and reverse proxies don't finish against a recorder.
		if strings.Contains(route.Path, "stream") || strings.HasSuffix(route.Path, "/events") || strings.Contains(route.Path, "proxy") || strings.Contains(route.Path, "/lab") {
			continue
		}
		if _, ok := missingResourceGETExceptions[route.Path]; ok {
			continue
		}
		parts := strings.Split(strings.ReplaceAll(route.Path, ":project_id", projectID), "/")
		for i, s := range parts {
			switch {
			case strings.HasPrefix(s, ":"):
				parts[i] = "does-not-exist"
			case strings.HasPrefix(s, "*"):
				parts[i] = "no/such/key"
			}
		}
		req := httptest.NewRequest(route.Method, strings.Join(parts, "/"), strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		checked++
		if rec.Code >= 500 {
			t.Errorf("%s %s with an unknown id answered %d: %s", route.Method, route.Path, rec.Code, rec.Body.String())
		}
		if route.Method == http.MethodGet && rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with an unknown id answered %d, want 404: %s", route.Path, rec.Code, rec.Body.String())
		}
	}
	if checked < 50 {
		t.Fatalf("only %d routes checked — the walk is not exercising the router", checked)
	}
}
