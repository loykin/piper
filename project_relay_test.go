package piper

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
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

// comboProjectClient implements both projectclient.Client and
// projectclient.StreamClient so a single stub can record which path a given
// route actually took.
type comboProjectClient struct {
	streamCalled bool
	streamPath   string
	doCalled     bool
	doPath       string
}

func (s *comboProjectClient) DoProjectRequest(_ context.Context, _ memberclient.AuthContext, _ project.ProjectRef, req projectclient.Request) (projectclient.Response, error) {
	s.doCalled = true
	s.doPath = req.Path
	return projectclient.Response{Status: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"remote":true}`)}, nil
}

func (s *comboProjectClient) ServeProjectHTTP(_ context.Context, _ memberclient.AuthContext, _ project.ProjectRef, w http.ResponseWriter, req *http.Request) error {
	s.streamCalled = true
	s.streamPath = req.URL.Path
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
	return nil
}

func TestRemoteStorageRoutesUseProjectHTTPStream(t *testing.T) {
	const projectID = "project-1"
	home := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	if err := home.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: "Remote", OwnerMemberID: "member-1"}); err != nil {
		t.Fatal(err)
	}
	combo := &comboProjectClient{}
	refFor := func(id string) project.ProjectRef {
		return project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: id}
	}
	router := home.newRouterWithFederation(nil, nil, nil, combo, refFor, nil, "")

	// Storage object download must go through the streaming relay, not the
	// buffered one — this is the route that used to fully buffer arbitrary-size
	// blobs in memory on both the request and response side.
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/storage/object?key=model.bin", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if !combo.streamCalled || combo.doCalled {
		t.Fatalf("storage GET: streamCalled=%v doCalled=%v, want stream only", combo.streamCalled, combo.doCalled)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("storage GET status=%d body=%s", rec.Code, rec.Body.String())
	}

	*combo = comboProjectClient{}
	uploadBody := &bytes.Buffer{}
	mw := multipart.NewWriter(uploadBody)
	fw, err := mw.CreateFormFile("file", "model.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/storage/object", uploadBody)
	uploadReq.Header.Set("Content-Type", mw.FormDataContentType())
	uploadRec := httptest.NewRecorder()
	router.ServeHTTP(uploadRec, uploadReq)
	if !combo.streamCalled || combo.doCalled {
		t.Fatalf("storage POST: streamCalled=%v doCalled=%v, want stream only", combo.streamCalled, combo.doCalled)
	}

	// A non-storage mutation (template submission) must still go through the
	// buffered, idempotency-key-aware relay — no regression from the storage split.
	*combo = comboProjectClient{}
	pipelineReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipelines", strings.NewReader(`{"yaml":"metadata:\n  name: x\nspec:\n  steps: []\n"}`))
	pipelineReq.Header.Set("Content-Type", "application/json")
	pipelineRec := httptest.NewRecorder()
	router.ServeHTTP(pipelineRec, pipelineReq)
	if !combo.doCalled || combo.streamCalled {
		t.Fatalf("pipelines POST: doCalled=%v streamCalled=%v, want buffered only", combo.doCalled, combo.streamCalled)
	}
	if combo.doPath != "/pipelines" {
		t.Fatalf("pipelines POST relayed path = %q, want /pipelines", combo.doPath)
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
