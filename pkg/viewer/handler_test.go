package viewer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/pkg/project"
)

func TestOpenViewerStatusDistinguishesCreateFromReuse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newFakeRepo()
	mgr := NewManager(repo, nil, t.TempDir())
	mgr.RegisterDriver(&fakeDriver{typ: "fake"})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: "project-a"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	NewHandler(mgr, repo).RegisterRoutes(router.Group(""))

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/runs/run-1/artifacts/train/model/view", strings.NewReader(`{"type":"fake"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	first := request()
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want %d: %s", first.Code, http.StatusCreated, first.Body.String())
	}
	for _, v := range repo.viewers {
		repo.findRunning = v
		break
	}
	second := request()
	if second.Code != http.StatusOK {
		t.Fatalf("reuse status = %d, want %d: %s", second.Code, http.StatusOK, second.Body.String())
	}
}

// Viewers are stored by id alone. A request under one project must not be
// able to read, stop, or proxy a viewer that belongs to another project.
func TestViewerRoutesAreScopedToTheURLProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newFakeRepo()
	repo.viewers["v-b"] = &Viewer{ID: "v-b", ProjectID: "project-b", Type: "fake", Status: StatusRunning}
	mgr := NewManager(repo, nil, t.TempDir())
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: "project-a"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := NewHandler(mgr, repo)
	h.RegisterRoutes(router.Group(""))
	h.RegisterProxyRoutes(router.Group(""))

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/viewers/v-b"},
		{http.MethodPost, "/viewers/v-b/stop"},
		{http.MethodGet, "/viewers/v-b/proxy/index.html"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s from another project = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
	if repo.viewers["v-b"].Status != StatusRunning {
		t.Fatal("another project's viewer was stopped")
	}
}
