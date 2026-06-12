package project

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/security"
)

type memberManagerStub struct {
	members []*security.ProjectMember
}

func (s *memberManagerStub) ListMembers(context.Context, string) ([]*security.ProjectMember, error) {
	return s.members, nil
}
func (s *memberManagerStub) AddMember(context.Context, *security.ProjectMember) error {
	return nil
}
func (s *memberManagerStub) GetMember(context.Context, string, string) (*security.ProjectMember, error) {
	return nil, nil
}
func (s *memberManagerStub) UpdateMember(context.Context, *security.ProjectMember) error {
	return nil
}
func (s *memberManagerStub) RemoveMember(context.Context, string, string) error {
	return nil
}

func TestMemberHandlerUsesProjectContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	manager := &memberManagerStub{members: []*security.ProjectMember{
		{ProjectID: "project-a", UserID: "user-a", Role: "admin"},
	}}
	group := router.Group("/projects/:project_id", func(c *gin.Context) {
		ctx := WithContext(c.Request.Context(), Context{
			ID:   c.Param("project_id"),
			Role: security.ProjectRoleAdmin,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	NewMemberHandler(manager).RegisterRoutes(group)

	req := httptest.NewRequest(http.MethodGet, "/projects/project-a/members", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
