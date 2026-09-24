package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// Executions are addressed by id alone: the detail and action routes resolve
// the owning notebook themselves instead of requiring it in the path.
func TestExecutionRoutesAreAddressedByID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newHarness(t, PolicyApprovalRequired)
	h.seedNotebook("nb.ipynb", 1)
	exec, _, err := h.svc.CreateExecution(context.Background(), adminActor, testProject, testNotebook, CreateExecutionRequest{Kind: KindNotebook, Path: "nb.ipynb"}, "")
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	router := gin.New()
	NewHandler(h.svc).RegisterRoutes(router.Group("/projects/:project_id", func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: testProject, Role: security.ProjectRoleAdmin})
		ctx = security.WithIdentity(ctx, &security.Identity{ID: adminActor.ID})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}))
	do := func(method, path string) int {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, "/projects/"+testProject+path, nil))
		return rec.Code
	}

	if code := do(http.MethodGet, "/notebook-executions/"+exec.ID); code != http.StatusOK {
		t.Fatalf("GET detail = %d, want 200", code)
	}
	if code := do(http.MethodGet, "/notebook-executions/missing"); code != http.StatusNotFound {
		t.Fatalf("GET unknown id = %d, want 404", code)
	}
	if code := do(http.MethodPost, "/notebook-executions/"+exec.ID+"/deny"); code != http.StatusNoContent {
		t.Fatalf("POST deny = %d, want 204", code)
	}
	got, err := h.svc.GetExecution(context.Background(), testProject, exec.ID)
	if err != nil || got.Status == StatusAwaitingApproval || !IsTerminalExecutionStatus(got.Status) {
		t.Fatalf("status after deny = %v (err %v), want a terminal status", got, err)
	}
}
