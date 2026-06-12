package project

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequireProjectContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &memoryRepo{projects: map[string]*Project{
		"team-a": {ID: "team-a", Name: "Team A"},
	}}
	router := gin.New()
	group := router.Group("/api/projects/:project_id", RequireTrusted(repo))
	group.GET("/check", func(c *gin.Context) {
		project, ok := FromContext(c.Request.Context())
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{"project_id": project.ID})
	})

	found := httptest.NewRecorder()
	router.ServeHTTP(found, httptest.NewRequest(http.MethodGet, "/api/projects/team-a/check", nil))
	if found.Code != http.StatusOK {
		t.Fatalf("found status = %d: %s", found.Code, found.Body.String())
	}

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/projects/missing/check", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", missing.Code)
	}
}
