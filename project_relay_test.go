package piper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/template"
)

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
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201: %s", rec.Code, rec.Body.String())
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
