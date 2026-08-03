package project

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/pkg/security"
)

type memberManagerStub struct {
	members []*security.ProjectMember
}

type userDirectoryStub struct {
	users []*security.User
}

func (s *userDirectoryStub) GetUser(_ context.Context, userID string) (*security.User, error) {
	for _, user := range s.users {
		if user.ID == userID {
			return user, nil
		}
	}
	return nil, nil
}

func (s *userDirectoryStub) ListUsers(context.Context) ([]*security.User, error) {
	return s.users, nil
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

func TestMemberCandidatesUseUsernamesAndExcludeExistingOrDisabledUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	manager := &memberManagerStub{members: []*security.ProjectMember{
		{ProjectID: "project-a", UserID: "existing-id", Role: "member"},
	}}
	users := &userDirectoryStub{users: []*security.User{
		{ID: "available-id", Username: "available-user"},
		{ID: "existing-id", Username: "existing-user"},
		{ID: "disabled-id", Username: "disabled-user", Disabled: true},
		{ID: "admin-id", Username: "system-admin", SystemAdmin: true},
	}}
	group := router.Group("/projects/:project_id", func(c *gin.Context) {
		ctx := WithContext(c.Request.Context(), Context{
			ID:   c.Param("project_id"),
			Role: security.ProjectRoleAdmin,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	NewMemberHandler(manager, users).RegisterRoutes(group)

	req := httptest.NewRequest(http.MethodGet, "/projects/project-a/members/candidates", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0]["username"] != "available-user" {
		t.Fatalf("candidates = %#v, want available username only", got)
	}
	if _, found := got[0]["user_id"]; found {
		t.Fatalf("candidate exposed internal user_id: %#v", got[0])
	}
}
