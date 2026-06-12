package project

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/security"
)

type memoryRepo struct {
	projects map[string]*Project
}

func (r *memoryRepo) Create(_ context.Context, p *Project) error {
	copyProject := *p
	r.projects[p.ID] = &copyProject
	return nil
}

func (r *memoryRepo) Get(_ context.Context, id string) (*Project, error) {
	return r.projects[id], nil
}

func (r *memoryRepo) List(context.Context) ([]*Project, error) {
	out := make([]*Project, 0, len(r.projects))
	for _, p := range r.projects {
		out = append(out, p)
	}
	return out, nil
}

func (r *memoryRepo) Delete(_ context.Context, id string) error {
	delete(r.projects, id)
	return nil
}

func TestProjectCRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &memoryRepo{projects: map[string]*Project{}}
	router := gin.New()
	NewHandler(repo, nil).RegisterRoutes(router.Group("/api"))

	body, _ := json.Marshal(map[string]string{"id": "team-a", "name": "Team A"})
	create := httptest.NewRecorder()
	router.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(body)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.Code, create.Body.String())
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/projects/team-a", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", get.Code, get.Body.String())
	}

	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, httptest.NewRequest(http.MethodDelete, "/api/projects/team-a", nil))
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestCreateProjectRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(&memoryRepo{projects: map[string]*Project{}}, nil).RegisterRoutes(router.Group("/api"))

	body, _ := json.Marshal(map[string]string{"id": "../team", "name": "Team"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

type systemAdminAuthorizer struct{}

func (systemAdminAuthorizer) ListProjectRoles(context.Context, *security.Identity) (map[string]security.ProjectRole, error) {
	return map[string]security.ProjectRole{}, nil
}

func (systemAdminAuthorizer) ProjectRole(context.Context, *security.Identity, string) (security.ProjectRole, error) {
	return security.ProjectRoleAdmin, nil
}

func (systemAdminAuthorizer) AuthorizeSystem(context.Context, *security.Identity) error {
	return nil
}

func TestProjectListUsesAuthorizerForSystemAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &memoryRepo{projects: map[string]*Project{
		"project-a": {ID: "project-a", Name: "Project A"},
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		identity := &security.Identity{ID: "external-admin", SystemAdmin: false}
		c.Request = c.Request.WithContext(security.WithIdentity(c.Request.Context(), identity))
		c.Next()
	})
	NewHandler(repo, systemAdminAuthorizer{}).RegisterRoutes(router.Group("/api"))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var projects []*Project
	if err := json.NewDecoder(rec.Body).Decode(&projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID != "project-a" {
		t.Fatalf("projects = %#v, want project-a", projects)
	}
}
