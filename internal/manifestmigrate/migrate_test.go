package manifestmigrate

import (
	"context"
	"testing"

	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
	"github.com/loykin/piper/pkg/template"
)

// ─── fake project.Repository ───────────────────────────────────────────────

type fakeProjectRepo struct {
	projects []*project.Project
}

func (r *fakeProjectRepo) Create(context.Context, *project.Project) error { return nil }
func (r *fakeProjectRepo) SetOwner(context.Context, string, string) error { return nil }
func (r *fakeProjectRepo) Get(_ context.Context, id string) (*project.Project, error) {
	for _, p := range r.projects {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil
}
func (r *fakeProjectRepo) List(context.Context) ([]*project.Project, error) { return r.projects, nil }
func (r *fakeProjectRepo) Delete(context.Context, string) error             { return nil }

// ─── fake template.Repository ──────────────────────────────────────────────

type fakeTemplateRepo struct {
	byProject map[string][]*template.Template
}

func newFakeTemplateRepo() *fakeTemplateRepo {
	return &fakeTemplateRepo{byProject: map[string][]*template.Template{}}
}

func (r *fakeTemplateRepo) NextVersion(_ context.Context, projectID, name string) (int, error) {
	max := 0
	for _, t := range r.byProject[projectID] {
		if t.Name == name && t.Version > max {
			max = t.Version
		}
	}
	return max + 1, nil
}

func (r *fakeTemplateRepo) Create(_ context.Context, t *template.Template) error {
	cp := *t
	r.byProject[t.ProjectID] = append(r.byProject[t.ProjectID], &cp)
	return nil
}

func (r *fakeTemplateRepo) Get(_ context.Context, projectID, id string) (*template.Template, error) {
	for _, t := range r.byProject[projectID] {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

func (r *fakeTemplateRepo) List(_ context.Context, projectID string, _ template.Filter) ([]*template.Template, error) {
	return r.byProject[projectID], nil
}

func (r *fakeTemplateRepo) Delete(context.Context, string, string) error { return nil }

// ─── fake notebook.Repository ───────────────────────────────────────────────

type fakeNotebookRepo struct {
	byProject map[string][]*notebook.NotebookServer
}

func newFakeNotebookRepo() *fakeNotebookRepo {
	return &fakeNotebookRepo{byProject: map[string][]*notebook.NotebookServer{}}
}

func (r *fakeNotebookRepo) Create(_ context.Context, nb *notebook.NotebookServer) error {
	cp := *nb
	r.byProject[nb.ProjectID] = append(r.byProject[nb.ProjectID], &cp)
	return nil
}
func (r *fakeNotebookRepo) Get(_ context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	for _, nb := range r.byProject[projectID] {
		if nb.Name == name {
			return nb, nil
		}
	}
	return nil, nil
}
func (r *fakeNotebookRepo) GetByVolumeID(context.Context, string, string) (*notebook.NotebookServer, error) {
	return nil, nil
}
func (r *fakeNotebookRepo) Update(_ context.Context, nb *notebook.NotebookServer) error {
	for i, cur := range r.byProject[nb.ProjectID] {
		if cur.Name == nb.Name {
			cp := *nb
			r.byProject[nb.ProjectID][i] = &cp
			return nil
		}
	}
	return nil
}
func (r *fakeNotebookRepo) SetStatus(context.Context, string, string, string) error { return nil }
func (r *fakeNotebookRepo) List(_ context.Context, projectID string) ([]*notebook.NotebookServer, error) {
	return r.byProject[projectID], nil
}
func (r *fakeNotebookRepo) Delete(context.Context, string, string) error { return nil }

// ─── fake serving.Repository ────────────────────────────────────────────────

type fakeServingRepo struct {
	byProject map[string][]*serving.Service
}

func newFakeServingRepo() *fakeServingRepo {
	return &fakeServingRepo{byProject: map[string][]*serving.Service{}}
}

func (r *fakeServingRepo) Create(_ context.Context, svc *serving.Service) error {
	cp := *svc
	r.byProject[svc.ProjectID] = append(r.byProject[svc.ProjectID], &cp)
	return nil
}
func (r *fakeServingRepo) Get(_ context.Context, projectID, name string) (*serving.Service, error) {
	for _, svc := range r.byProject[projectID] {
		if svc.Name == name {
			return svc, nil
		}
	}
	return nil, nil
}
func (r *fakeServingRepo) Update(_ context.Context, svc *serving.Service) error {
	for i, cur := range r.byProject[svc.ProjectID] {
		if cur.Name == svc.Name {
			cp := *svc
			r.byProject[svc.ProjectID][i] = &cp
			return nil
		}
	}
	return nil
}
func (r *fakeServingRepo) Upsert(context.Context, *serving.Service) error          { return nil }
func (r *fakeServingRepo) SetStatus(context.Context, string, string, string) error { return nil }
func (r *fakeServingRepo) SetStatusEndpoint(context.Context, string, string, string, string) error {
	return nil
}
func (r *fakeServingRepo) List(_ context.Context, projectID string) ([]*serving.Service, error) {
	return r.byProject[projectID], nil
}
func (r *fakeServingRepo) Delete(context.Context, string, string) error { return nil }
func (r *fakeServingRepo) ListHistory(context.Context, string) ([]*serving.ServiceHistory, error) {
	return nil, nil
}

// ─── tests ──────────────────────────────────────────────────────────────────

const staleNotebookYAML = `apiVersion: piper/v1
kind: Notebook
metadata:
  name: nb
spec:
  driver:
    placement:
      worker: gpu-1
`

const staleServiceYAML = `apiVersion: piper/v1
kind: ModelService
metadata:
  name: svc
spec:
  driver:
    placement:
      worker: serving-agent
`

const stalePipelineYAML = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: pl
spec:
  defaults:
    driver:
      placement:
        worker: pipeline-worker-1
`

const cleanNotebookYAML = `apiVersion: piper/v1
kind: Notebook
metadata:
  name: nb-clean
spec:
  driver:
    placement:
      runtime: baremetal
`

func newTestRepos(t *testing.T) (*storemod.Repos, *fakeTemplateRepo, *fakeNotebookRepo, *fakeServingRepo) {
	t.Helper()
	tmpl := newFakeTemplateRepo()
	nb := newFakeNotebookRepo()
	svc := newFakeServingRepo()
	repos := &storemod.Repos{
		Project:          &fakeProjectRepo{projects: []*project.Project{{ID: "proj-a"}, {ID: "proj-b"}}},
		PipelineTemplate: tmpl,
		Notebook:         nb,
		Serving:          svc,
	}
	return repos, tmpl, nb, svc
}

func TestScan_FindsStaleFieldsAcrossAllDomainsAndProjects(t *testing.T) {
	repos, tmpl, nb, svc := newTestRepos(t)
	ctx := context.Background()

	_ = tmpl.Create(ctx, &template.Template{ProjectID: "proj-a", Name: "pl", Version: 1, YAML: stalePipelineYAML})
	_ = nb.Create(ctx, &notebook.NotebookServer{ProjectID: "proj-a", Name: "nb", YAML: staleNotebookYAML})
	_ = svc.Create(ctx, &serving.Service{ProjectID: "proj-b", Name: "svc", YAML: staleServiceYAML})
	_ = nb.Create(ctx, &notebook.NotebookServer{ProjectID: "proj-b", Name: "nb-clean", YAML: cleanNotebookYAML})

	findings, err := Scan(ctx, repos)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %d, want 3: %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Fixed {
			t.Errorf("Scan must never fix anything, but %+v is marked fixed", f)
		}
	}

	// Nothing in the fakes was mutated by Scan.
	if tmpl.byProject["proj-a"][0].YAML != stalePipelineYAML {
		t.Fatal("Scan mutated stored template YAML")
	}
}

func TestApply_FixesNotebookAndServiceInPlace(t *testing.T) {
	repos, _, nb, svc := newTestRepos(t)
	ctx := context.Background()

	_ = nb.Create(ctx, &notebook.NotebookServer{ProjectID: "proj-a", Name: "nb", YAML: staleNotebookYAML})
	_ = svc.Create(ctx, &serving.Service{ProjectID: "proj-b", Name: "svc", YAML: staleServiceYAML})

	findings, err := Apply(ctx, repos)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2: %+v", len(findings), findings)
	}
	for _, f := range findings {
		if !f.Fixed || f.Err != nil {
			t.Errorf("finding not fixed: %+v", f)
		}
	}

	got, _ := nb.Get(ctx, "proj-a", "nb")
	if got.YAML == staleNotebookYAML {
		t.Fatal("notebook YAML was not rewritten")
	}
	if _, changed, _ := StripPlacementWorkerLabel(got.YAML); changed {
		t.Fatal("notebook YAML still contains a placement.worker field")
	}

	gotSvc, _ := svc.Get(ctx, "proj-b", "svc")
	if _, changed, _ := StripPlacementWorkerLabel(gotSvc.YAML); changed {
		t.Fatal("service YAML still contains a placement.worker field")
	}
}

func TestApply_PipelineCreatesNewVersionAndKeepsOldOneImmutable(t *testing.T) {
	repos, tmpl, _, _ := newTestRepos(t)
	ctx := context.Background()

	_ = tmpl.Create(ctx, &template.Template{ProjectID: "proj-a", Name: "pl", Version: 1, YAML: stalePipelineYAML})

	findings, err := Apply(ctx, repos)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if !f.Fixed || f.NewVersion != 2 {
		t.Fatalf("finding = %+v, want Fixed=true NewVersion=2", f)
	}

	all, _ := tmpl.List(ctx, "proj-a", template.Filter{})
	if len(all) != 2 {
		t.Fatalf("templates = %d, want 2 (old + new version)", len(all))
	}
	var v1, v2 *template.Template
	for _, t := range all {
		if t.Version == 1 {
			v1 = t
		}
		if t.Version == 2 {
			v2 = t
		}
	}
	if v1 == nil || v1.YAML != stalePipelineYAML {
		t.Fatal("old version 1 must remain untouched")
	}
	if v2 == nil {
		t.Fatal("expected a new version 2 to be created")
	}
	if _, changed, _ := StripPlacementWorkerLabel(v2.YAML); changed {
		t.Fatal("new version still contains a placement.worker field")
	}
}

func TestApply_OnlyFixesLatestPipelineVersion(t *testing.T) {
	repos, tmpl, _, _ := newTestRepos(t)
	ctx := context.Background()

	_ = tmpl.Create(ctx, &template.Template{ProjectID: "proj-a", Name: "pl", Version: 1, YAML: stalePipelineYAML})
	_ = tmpl.Create(ctx, &template.Template{ProjectID: "proj-a", Name: "pl", Version: 2, YAML: cleanNotebookYAML}) // reuse a clean fixture as "latest, already fine"

	findings, err := Apply(ctx, repos)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0 (latest version is already clean): %+v", len(findings), findings)
	}
}
