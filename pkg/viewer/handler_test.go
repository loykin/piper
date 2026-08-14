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
