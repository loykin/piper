package piper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/template"
)

type streamProjectStub struct{ called bool }

func (*streamProjectStub) DoProjectRequest(context.Context, memberclient.AuthContext, project.ProjectRef, projectclient.Request) (projectclient.Response, error) {
	return projectclient.Response{}, nil
}

type projectRequestStub struct {
	called bool
	path   string
}

func (s *projectRequestStub) DoProjectRequest(_ context.Context, _ memberclient.AuthContext, _ project.ProjectRef, req projectclient.Request) (projectclient.Response, error) {
	s.called = true
	s.path = req.Path
	return projectclient.Response{Status: http.StatusTeapot, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"remote":true}`)}, nil
}

func (s *streamProjectStub) ServeProjectHTTP(_ context.Context, auth memberclient.AuthContext, ref project.ProjectRef, w http.ResponseWriter, req *http.Request) error {
	s.called = true
	if auth.Role != security.ProjectRoleAdmin || ref.MemberID != "member-1" || req.URL.Path != "/projects/project-1/notebooks/demo/proxy/api" {
		return fmt.Errorf("unexpected stream auth=%+v ref=%+v path=%q", auth, ref, req.URL.Path)
	}
	w.Header().Set("X-Relayed-Member", ref.MemberID)
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func TestRemoteProjectPipelineAPIWritesOnlyMemberRepository(t *testing.T) {
	const (
		projectID = "project-1"
		memberID  = "member-1"
	)
	home := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	member := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	if err := home.repos.Project.Create(context.Background(), &project.Project{
		ID: projectID, Name: "Remote Project", OwnerMemberID: memberID,
	}); err != nil {
		t.Fatal(err)
	}
	refFor := func(id string) project.ProjectRef {
		return project.ProjectRef{HomeID: "home-1", MemberID: memberID, ProjectID: id}
	}
	router := home.newRouterWithFederation(nil, nil, nil, NewLocalMemberClient(member), refFor, nil, "")
	body := `{"yaml":"apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: member-owned\nspec:\n  steps:\n    - name: hello\n      run:\n        command: [\\\"echo\\\", \\\"hello\\\"]\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipelines", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "pipeline-submit-1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipelines", strings.NewReader(body))
	retryReq.Header.Set("Content-Type", "application/json")
	retryReq.Header.Set("Idempotency-Key", "pipeline-submit-1")
	retryRec := httptest.NewRecorder()
	router.ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusCreated || retryRec.Body.String() != rec.Body.String() {
		t.Fatalf("idempotent retry status=%d body=%s, first=%s", retryRec.Code, retryRec.Body.String(), rec.Body.String())
	}
	conflictReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipelines", strings.NewReader(strings.Replace(body, "member-owned", "different", 1)))
	conflictReq.Header.Set("Content-Type", "application/json")
	conflictReq.Header.Set("Idempotency-Key", "pipeline-submit-1")
	conflictRec := httptest.NewRecorder()
	router.ServeHTTP(conflictRec, conflictReq)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("changed retry status=%d body=%s", conflictRec.Code, conflictRec.Body.String())
	}

	memberTemplates, err := member.repos.PipelineTemplate.List(context.Background(), projectID, template.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(memberTemplates) != 1 || memberTemplates[0].Name != "member-owned" {
		t.Fatalf("member templates = %+v, want one member-owned template", memberTemplates)
	}
	homeTemplates, err := home.repos.PipelineTemplate.List(context.Background(), projectID, template.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(homeTemplates) != 0 {
		t.Fatalf("template leaked into Home repository: %+v", homeTemplates)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipelines", nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "member-owned") {
		t.Fatalf("GET status=%d body=%s", listRec.Code, listRec.Body.String())
	}
}

func TestRemoteMemberRouteGroupFailsClosedForNewPath(t *testing.T) {
	const projectID = "project-1"
	home := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	if err := home.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: "Remote", OwnerMemberID: "member-1"}); err != nil {
		t.Fatal(err)
	}
	stub := &projectRequestStub{}
	router := gin.New()
	group := router.Group("/api/projects/:project_id", project.Require(home.repos.Project, nil, security.ProjectRoleViewer))
	memberRoutes := group.Group("", relayRemoteProject(stub, func(id string) project.ProjectRef {
		return project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: id}
	}))
	memberRoutes.GET("/future-domain/resource", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"local": true})
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/future-domain/resource", nil))
	if !stub.called || stub.path != "/future-domain/resource" {
		t.Fatalf("remote relay called=%v path=%q", stub.called, stub.path)
	}
	if rec.Code != http.StatusTeapot || !strings.Contains(rec.Body.String(), `"remote":true`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMemberProjectRouterRechecksDelegatedMutationRole(t *testing.T) {
	member := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	client := NewLocalMemberClient(member)
	response, err := client.DoProjectRequest(context.Background(),
		memberclient.AuthContext{ActorID: "viewer-1", Role: security.ProjectRoleViewer},
		project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "project-1"},
		projectclient.Request{
			Method: http.MethodPost,
			Path:   "/pipelines",
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body:   []byte(`{"yaml":"metadata:\n  name: forbidden\nspec:\n  steps: []\n"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Status, response.Body)
	}
	templates, err := member.repos.PipelineTemplate.List(context.Background(), "project-1", template.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 0 {
		t.Fatalf("viewer mutation reached repository: %+v", templates)
	}
}

func TestRemoteBrowserProxyUsesProjectHTTPStream(t *testing.T) {
	home := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	if err := home.repos.Project.Create(context.Background(), &project.Project{ID: "project-1", Name: "Remote", OwnerMemberID: "member-1"}); err != nil {
		t.Fatal(err)
	}
	stream := &streamProjectStub{}
	refFor := func(id string) project.ProjectRef {
		return project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: id}
	}
	router := home.newRouterWithFederation(nil, nil, nil, stream, refFor, nil, "")
	req := httptest.NewRequest(http.MethodGet, "/projects/project-1/notebooks/demo/proxy/api", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if !stream.called || rec.Code != http.StatusAccepted || rec.Header().Get("X-Relayed-Member") != "member-1" {
		t.Fatalf("called=%v status=%d header=%q body=%s", stream.called, rec.Code, rec.Header().Get("X-Relayed-Member"), rec.Body.String())
	}
}
