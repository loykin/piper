package piper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/projectcleanup"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
)

// -- minimal project.Repository fake --

type cleanupTestProjectRepo struct {
	projects map[string]*project.Project
}

func newCleanupTestProjectRepo(projects ...*project.Project) *cleanupTestProjectRepo {
	m := make(map[string]*project.Project, len(projects))
	for _, p := range projects {
		m[p.ID] = p
	}
	return &cleanupTestProjectRepo{projects: m}
}
func (r *cleanupTestProjectRepo) Create(_ context.Context, p *project.Project) error {
	r.projects[p.ID] = p
	return nil
}
func (r *cleanupTestProjectRepo) Get(_ context.Context, id string) (*project.Project, error) {
	return r.projects[id], nil
}
func (r *cleanupTestProjectRepo) List(_ context.Context) ([]*project.Project, error) {
	out := make([]*project.Project, 0, len(r.projects))
	for _, p := range r.projects {
		out = append(out, p)
	}
	return out, nil
}
func (r *cleanupTestProjectRepo) SetOwner(_ context.Context, id, memberID string) error {
	if p, ok := r.projects[id]; ok {
		p.OwnerMemberID = memberID
	}
	return nil
}
func (r *cleanupTestProjectRepo) Delete(_ context.Context, id string) error {
	delete(r.projects, id)
	return nil
}

// -- minimal notebook fakes --

type cleanupTestNotebookRepo struct {
	servers map[string]*notebook.NotebookServer
}

func newCleanupTestNotebookRepo(servers ...*notebook.NotebookServer) *cleanupTestNotebookRepo {
	m := make(map[string]*notebook.NotebookServer, len(servers))
	for _, nb := range servers {
		m[nb.ProjectID+"/"+nb.Name] = nb
	}
	return &cleanupTestNotebookRepo{servers: m}
}
func (r *cleanupTestNotebookRepo) key(projectID, name string) string { return projectID + "/" + name }
func (r *cleanupTestNotebookRepo) Create(_ context.Context, nb *notebook.NotebookServer) error {
	r.servers[r.key(nb.ProjectID, nb.Name)] = nb
	return nil
}
func (r *cleanupTestNotebookRepo) Get(_ context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	return r.servers[r.key(projectID, name)], nil
}
func (r *cleanupTestNotebookRepo) GetByVolumeID(_ context.Context, projectID, volumeID string) (*notebook.NotebookServer, error) {
	for _, nb := range r.servers {
		if nb.ProjectID == projectID && nb.VolumeID == volumeID {
			return nb, nil
		}
	}
	return nil, nil
}
func (r *cleanupTestNotebookRepo) Update(_ context.Context, nb *notebook.NotebookServer) error {
	r.servers[r.key(nb.ProjectID, nb.Name)] = nb
	return nil
}
func (r *cleanupTestNotebookRepo) SetStatus(_ context.Context, projectID, name, status string) error {
	if nb, ok := r.servers[r.key(projectID, name)]; ok {
		nb.Status = status
	}
	return nil
}
func (r *cleanupTestNotebookRepo) List(_ context.Context, projectID string) ([]*notebook.NotebookServer, error) {
	var out []*notebook.NotebookServer
	for _, nb := range r.servers {
		if nb.ProjectID == projectID {
			out = append(out, nb)
		}
	}
	return out, nil
}
func (r *cleanupTestNotebookRepo) Delete(_ context.Context, projectID, name string) error {
	delete(r.servers, r.key(projectID, name))
	return nil
}
func (r *cleanupTestNotebookRepo) AppendHistory(context.Context, *notebook.NotebookServer) error {
	return nil
}
func (r *cleanupTestNotebookRepo) ListHistory(context.Context, string, int, int) ([]*notebook.NotebookHistory, error) {
	return nil, nil
}
func (r *cleanupTestNotebookRepo) CountHistory(context.Context, string) (int, error) { return 0, nil }
func (r *cleanupTestNotebookRepo) PurgeHistoryBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type cleanupTestVolumeRepo struct {
	volumes map[string]*notebook.NotebookVolume
}

func newCleanupTestVolumeRepo() *cleanupTestVolumeRepo {
	return &cleanupTestVolumeRepo{volumes: map[string]*notebook.NotebookVolume{}}
}
func (r *cleanupTestVolumeRepo) Create(_ context.Context, v *notebook.NotebookVolume) error {
	r.volumes[v.ID] = v
	return nil
}
func (r *cleanupTestVolumeRepo) Get(_ context.Context, id string) (*notebook.NotebookVolume, error) {
	return r.volumes[id], nil
}
func (r *cleanupTestVolumeRepo) List(_ context.Context, projectID string, _, _ int) ([]*notebook.NotebookVolume, error) {
	var out []*notebook.NotebookVolume
	for _, v := range r.volumes {
		if v.ProjectID == projectID {
			out = append(out, v)
		}
	}
	return out, nil
}
func (r *cleanupTestVolumeRepo) Count(_ context.Context, projectID string) (int, error) {
	n := 0
	for _, v := range r.volumes {
		if v.ProjectID == projectID {
			n++
		}
	}
	return n, nil
}
func (r *cleanupTestVolumeRepo) Update(_ context.Context, v *notebook.NotebookVolume) error {
	r.volumes[v.ID] = v
	return nil
}
func (r *cleanupTestVolumeRepo) SetStatus(_ context.Context, id, status string) error {
	if v, ok := r.volumes[id]; ok {
		v.Status = status
	}
	return nil
}
func (r *cleanupTestVolumeRepo) Delete(_ context.Context, id string) error {
	delete(r.volumes, id)
	return nil
}

type cleanupTestNotebookDriver struct {
	stopErr error
}

func (d *cleanupTestNotebookDriver) ProvisionVolume(context.Context, *notebook.NotebookVolume, notebook.Notebook) error {
	return nil
}
func (d *cleanupTestNotebookDriver) Start(context.Context, notebook.Notebook, *notebook.NotebookVolume, string) (*notebook.NotebookServer, error) {
	return nil, errors.New("not implemented")
}
func (d *cleanupTestNotebookDriver) Stop(context.Context, *notebook.NotebookServer) error {
	return d.stopErr
}
func (d *cleanupTestNotebookDriver) DeprovisionVolume(context.Context, *notebook.NotebookVolume) error {
	return nil
}

// -- minimal serving fakes --

type cleanupTestServiceRepo struct {
	services   map[string]*serving.Service
	listCalled bool
}

func newCleanupTestServiceRepo(svcs ...*serving.Service) *cleanupTestServiceRepo {
	m := make(map[string]*serving.Service, len(svcs))
	for _, s := range svcs {
		m[s.ProjectID+"/"+s.Name] = s
	}
	return &cleanupTestServiceRepo{services: m}
}
func (r *cleanupTestServiceRepo) key(projectID, name string) string { return projectID + "/" + name }
func (r *cleanupTestServiceRepo) Create(_ context.Context, svc *serving.Service) error {
	r.services[r.key(svc.ProjectID, svc.Name)] = svc
	return nil
}
func (r *cleanupTestServiceRepo) Get(_ context.Context, projectID, name string) (*serving.Service, error) {
	return r.services[r.key(projectID, name)], nil
}
func (r *cleanupTestServiceRepo) Update(_ context.Context, svc *serving.Service) error {
	r.services[r.key(svc.ProjectID, svc.Name)] = svc
	return nil
}
func (r *cleanupTestServiceRepo) Upsert(_ context.Context, svc *serving.Service) error {
	r.services[r.key(svc.ProjectID, svc.Name)] = svc
	return nil
}
func (r *cleanupTestServiceRepo) SetStatus(_ context.Context, projectID, name, status string) error {
	if s, ok := r.services[r.key(projectID, name)]; ok {
		s.Status = status
	}
	return nil
}
func (r *cleanupTestServiceRepo) SetStatusEndpoint(_ context.Context, projectID, name, status, endpoint string) error {
	if s, ok := r.services[r.key(projectID, name)]; ok {
		s.Status = status
		s.Endpoint = endpoint
	}
	return nil
}
func (r *cleanupTestServiceRepo) List(_ context.Context, projectID string, _, _ int) ([]*serving.Service, error) {
	r.listCalled = true
	var out []*serving.Service
	for _, s := range r.services {
		if s.ProjectID == projectID {
			out = append(out, s)
		}
	}
	return out, nil
}
func (r *cleanupTestServiceRepo) Count(_ context.Context, projectID string) (int, error) {
	n := 0
	for _, s := range r.services {
		if s.ProjectID == projectID {
			n++
		}
	}
	return n, nil
}
func (r *cleanupTestServiceRepo) Delete(_ context.Context, projectID, name string) error {
	delete(r.services, r.key(projectID, name))
	return nil
}
func (r *cleanupTestServiceRepo) AppendHistory(context.Context, *serving.Service) error { return nil }
func (r *cleanupTestServiceRepo) ListHistory(context.Context, string, int, int) ([]*serving.ServiceHistory, error) {
	return nil, nil
}
func (r *cleanupTestServiceRepo) CountHistory(context.Context, string) (int, error) { return 0, nil }
func (r *cleanupTestServiceRepo) PurgeHistoryBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type cleanupTestServiceDriver struct{}

func (d *cleanupTestServiceDriver) ArtifactTarget() artifact.Target { return artifact.TargetLocal }
func (d *cleanupTestServiceDriver) Deploy(context.Context, serving.ModelService, artifact.Resolved, string) (*serving.Service, error) {
	return nil, errors.New("not implemented")
}
func (d *cleanupTestServiceDriver) Stop(context.Context, *serving.Service) error { return nil }

// TestProjectDeleteAbortsWhenNotebookCleanupFails proves BE-1's finalizer
// contract end to end through the real HTTP DELETE /projects/:project_id
// endpoint (pkg/project.Handler.delete): when the BeforeDelete hook's
// resource cleanup fails (here, a notebook's driver.Stop erroring), the
// handler converts the error to a 503, the delete never reaches
// repo.Delete, and every resource cleanup did or didn't touch stays
// consistent with that: the project row survives, the notebook row survives
// (notebook.Manager.Delete aborts before its own repo.Delete), and service
// cleanup — ordered after notebook cleanup — never even runs.
func TestProjectDeleteAbortsWhenNotebookCleanupFails(t *testing.T) {
	const projectID = "proj-with-live-notebook"

	projectRepo := newCleanupTestProjectRepo(
		&project.Project{ID: project.DefaultID, Name: "default"},
		&project.Project{ID: projectID, Name: projectID},
	)

	nbRepo := newCleanupTestNotebookRepo(&notebook.NotebookServer{
		ProjectID: projectID, Name: "live-nb", Status: notebook.StatusRunning,
	})
	volRepo := newCleanupTestVolumeRepo()
	stopErr := errors.New("driver unavailable")
	nbMgr := notebook.New(nbRepo, volRepo, &cleanupTestNotebookDriver{stopErr: stopErr})

	svcRepo := newCleanupTestServiceRepo(&serving.Service{
		ProjectID: projectID, Name: "live-svc", Status: serving.StatusRunning,
	})
	svcMgr := serving.New(svcRepo, &cleanupTestServiceDriver{})

	cleanupDeps := projectcleanup.Deps{
		Notebooks:    nbMgr,
		NotebookRepo: nbRepo,
		Volumes:      volRepo,
		Services:     svcMgr,
		ServiceRepo:  svcRepo,
	}

	handler := project.NewHandlerWithDirectory(projectRepo, nil, nil, nil)
	handler.WithBeforeDelete(func(ctx context.Context, value *project.Project) error {
		return cleanupDeps.CleanupLiveResources(ctx, value.ID)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler.RegisterRoutes(r.Group("/api"))

	req := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	if got, _ := projectRepo.Get(context.Background(), projectID); got == nil {
		t.Fatal("project row was deleted despite failed cleanup")
	}
	if got, _ := nbRepo.Get(context.Background(), projectID, "live-nb"); got == nil {
		t.Fatal("notebook row was deleted despite driver.Stop failure")
	}
	if svcRepo.listCalled {
		t.Fatal("service cleanup ran even though notebook cleanup aborted first")
	}
	if got, _ := svcRepo.Get(context.Background(), projectID, "live-svc"); got == nil {
		t.Fatal("service row was deleted even though cleanup aborted before service cleanup ran")
	}
}

// TestProjectDeleteSucceedsAfterCleanupSucceeds is the success-path
// counterpart: once every live notebook/service is cleaned up without
// error, the project delete proceeds and the row is actually gone.
func TestProjectDeleteSucceedsAfterCleanupSucceeds(t *testing.T) {
	const projectID = "proj-clean"

	projectRepo := newCleanupTestProjectRepo(
		&project.Project{ID: project.DefaultID, Name: "default"},
		&project.Project{ID: projectID, Name: projectID},
	)

	nbRepo := newCleanupTestNotebookRepo(&notebook.NotebookServer{
		ProjectID: projectID, Name: "live-nb", Status: notebook.StatusRunning,
	})
	volRepo := newCleanupTestVolumeRepo()
	nbMgr := notebook.New(nbRepo, volRepo, &cleanupTestNotebookDriver{})

	svcRepo := newCleanupTestServiceRepo(&serving.Service{
		ProjectID: projectID, Name: "live-svc", Status: serving.StatusRunning,
	})
	svcMgr := serving.New(svcRepo, &cleanupTestServiceDriver{})

	cleanupDeps := projectcleanup.Deps{
		Notebooks:    nbMgr,
		NotebookRepo: nbRepo,
		Volumes:      volRepo,
		Services:     svcMgr,
		ServiceRepo:  svcRepo,
	}

	handler := project.NewHandlerWithDirectory(projectRepo, nil, nil, nil)
	handler.WithBeforeDelete(func(ctx context.Context, value *project.Project) error {
		return cleanupDeps.CleanupLiveResources(ctx, value.ID)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler.RegisterRoutes(r.Group("/api"))

	req := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if got, _ := projectRepo.Get(context.Background(), projectID); got != nil {
		t.Fatal("project row survived a successful cleanup+delete")
	}
	if got, _ := nbRepo.Get(context.Background(), projectID, "live-nb"); got != nil {
		t.Fatal("notebook row survived cleanup")
	}
	if got, _ := svcRepo.Get(context.Background(), projectID, "live-svc"); got != nil {
		t.Fatal("service row survived cleanup")
	}
}
