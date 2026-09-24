package piper

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/viewer"
)

// TestListEndpointsNeverReturnNull walks every parameter-free (other than
// :project_id) GET route on an empty installation and asserts none answers
// 200 with a JSON `null` body. Go's nil slices encode as null, and the
// frontend treats a non-array list response as a contract violation rather
// than silently rendering it as an empty list.
func TestListEndpointsNeverReturnNull(t *testing.T) {
	const projectID = "contract-project"
	p := newTestPiper(t, Config{OutputDir: t.TempDir(), Runtime: RuntimeConfig{Type: RuntimeBaremetal}})
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: "Contract"}); err != nil {
		t.Fatal(err)
	}
	// Home mode (as `piper server` runs) so federation directory lists are covered too.
	engine := p.newRouterWithFederation(nil, viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir), nil, nil, nil, nil, "home-test").(*gin.Engine)
	checked := 0
	for _, route := range engine.Routes() {
		if route.Method != http.MethodGet {
			continue
		}
		path := strings.ReplaceAll(route.Path, ":project_id", projectID)
		// Path params other than :project_id need fixture data; SSE routes
		// never finish against a ResponseRecorder.
		if strings.ContainsAny(path, ":*") || strings.HasSuffix(path, "/events") || strings.Contains(path, "stream") {
			continue
		}
		for _, query := range []string{"", "?limit=20&offset=0"} {
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+query, nil))
			if rec.Code != http.StatusOK {
				continue
			}
			checked++
			if bytes.Equal(bytes.TrimSpace(rec.Body.Bytes()), []byte("null")) {
				t.Errorf("GET %s%s returned null; list endpoints must return []", route.Path, query)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no GET routes answered 200 — the walk is not exercising anything")
	}
}
