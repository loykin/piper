package projectcleanup

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/queue"
	"github.com/loykin/piper/internal/runlifecycle"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/serving"
	"github.com/loykin/piper/pkg/storage"
	"github.com/loykin/piper/pkg/template"
)

// ---- notebook fakes ----

type fakeNotebookRepo struct {
	servers map[string]*notebook.NotebookServer // key: projectID+"/"+name
}

func newFakeNotebookRepo(servers ...*notebook.NotebookServer) *fakeNotebookRepo {
	m := make(map[string]*notebook.NotebookServer, len(servers))
	for _, nb := range servers {
		m[nb.ProjectID+"/"+nb.Name] = nb
	}
	return &fakeNotebookRepo{servers: m}
}
func (r *fakeNotebookRepo) key(projectID, name string) string { return projectID + "/" + name }
func (r *fakeNotebookRepo) Create(_ context.Context, nb *notebook.NotebookServer) error {
	r.servers[r.key(nb.ProjectID, nb.Name)] = nb
	return nil
}
func (r *fakeNotebookRepo) Get(_ context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	return r.servers[r.key(projectID, name)], nil
}
func (r *fakeNotebookRepo) GetByVolumeID(_ context.Context, projectID, volumeID string) (*notebook.NotebookServer, error) {
	for _, nb := range r.servers {
		if nb.ProjectID == projectID && nb.VolumeID == volumeID {
			return nb, nil
		}
	}
	return nil, nil
}
func (r *fakeNotebookRepo) Update(_ context.Context, nb *notebook.NotebookServer) error {
	r.servers[r.key(nb.ProjectID, nb.Name)] = nb
	return nil
}
func (r *fakeNotebookRepo) SetStatus(_ context.Context, projectID, name, status string) error {
	if nb, ok := r.servers[r.key(projectID, name)]; ok {
		nb.Status = status
	}
	return nil
}
func (r *fakeNotebookRepo) List(_ context.Context, projectID string) ([]*notebook.NotebookServer, error) {
	var out []*notebook.NotebookServer
	for _, nb := range r.servers {
		if nb.ProjectID == projectID {
			out = append(out, nb)
		}
	}
	return out, nil
}
func (r *fakeNotebookRepo) Delete(_ context.Context, projectID, name string) error {
	delete(r.servers, r.key(projectID, name))
	return nil
}
func (r *fakeNotebookRepo) AppendHistory(context.Context, *notebook.NotebookServer) error { return nil }
func (r *fakeNotebookRepo) ListHistory(context.Context, string, int, int) ([]*notebook.NotebookHistory, error) {
	return nil, nil
}
func (r *fakeNotebookRepo) CountHistory(context.Context, string) (int, error) { return 0, nil }
func (r *fakeNotebookRepo) PurgeHistoryBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type fakeVolumeRepo struct {
	volumes    map[string]*notebook.NotebookVolume
	deprovOK   []string
	deletedIDs []string
}

func newFakeVolumeRepo(vols ...*notebook.NotebookVolume) *fakeVolumeRepo {
	m := make(map[string]*notebook.NotebookVolume, len(vols))
	for _, v := range vols {
		m[v.ID] = v
	}
	return &fakeVolumeRepo{volumes: m}
}
func (r *fakeVolumeRepo) Create(_ context.Context, v *notebook.NotebookVolume) error {
	r.volumes[v.ID] = v
	return nil
}
func (r *fakeVolumeRepo) Get(_ context.Context, id string) (*notebook.NotebookVolume, error) {
	return r.volumes[id], nil
}
func (r *fakeVolumeRepo) List(_ context.Context, projectID string, _, _ int) ([]*notebook.NotebookVolume, error) {
	var out []*notebook.NotebookVolume
	for _, v := range r.volumes {
		if v.ProjectID == projectID {
			out = append(out, v)
		}
	}
	return out, nil
}
func (r *fakeVolumeRepo) Count(_ context.Context, projectID string) (int, error) {
	n := 0
	for _, v := range r.volumes {
		if v.ProjectID == projectID {
			n++
		}
	}
	return n, nil
}
func (r *fakeVolumeRepo) Update(_ context.Context, v *notebook.NotebookVolume) error {
	r.volumes[v.ID] = v
	return nil
}
func (r *fakeVolumeRepo) SetStatus(_ context.Context, id, status string) error {
	if v, ok := r.volumes[id]; ok {
		v.Status = status
	}
	return nil
}
func (r *fakeVolumeRepo) Delete(_ context.Context, id string) error {
	r.deletedIDs = append(r.deletedIDs, id)
	delete(r.volumes, id)
	return nil
}

type fakeNotebookDriver struct {
	stopErr error
}

func (d *fakeNotebookDriver) ProvisionVolume(context.Context, *notebook.NotebookVolume, notebook.Notebook) error {
	return nil
}
func (d *fakeNotebookDriver) Start(context.Context, notebook.Notebook, *notebook.NotebookVolume, string) (*notebook.NotebookServer, error) {
	return nil, errors.New("not implemented")
}
func (d *fakeNotebookDriver) Stop(context.Context, *notebook.NotebookServer) error { return d.stopErr }
func (d *fakeNotebookDriver) DeprovisionVolume(context.Context, *notebook.NotebookVolume) error {
	return nil
}

// ---- serving fakes ----

type fakeServiceRepo struct {
	services   map[string]*serving.Service
	listCalled bool
}

func newFakeServiceRepo(svcs ...*serving.Service) *fakeServiceRepo {
	m := make(map[string]*serving.Service, len(svcs))
	for _, s := range svcs {
		m[s.ProjectID+"/"+s.Name] = s
	}
	return &fakeServiceRepo{services: m}
}
func (r *fakeServiceRepo) key(projectID, name string) string { return projectID + "/" + name }
func (r *fakeServiceRepo) Create(_ context.Context, svc *serving.Service) error {
	r.services[r.key(svc.ProjectID, svc.Name)] = svc
	return nil
}
func (r *fakeServiceRepo) Get(_ context.Context, projectID, name string) (*serving.Service, error) {
	return r.services[r.key(projectID, name)], nil
}
func (r *fakeServiceRepo) Update(_ context.Context, svc *serving.Service) error {
	r.services[r.key(svc.ProjectID, svc.Name)] = svc
	return nil
}
func (r *fakeServiceRepo) Upsert(_ context.Context, svc *serving.Service) error {
	r.services[r.key(svc.ProjectID, svc.Name)] = svc
	return nil
}
func (r *fakeServiceRepo) SetStatus(_ context.Context, projectID, name, status string) error {
	if s, ok := r.services[r.key(projectID, name)]; ok {
		s.Status = status
	}
	return nil
}
func (r *fakeServiceRepo) SetStatusEndpoint(_ context.Context, projectID, name, status, endpoint string) error {
	if s, ok := r.services[r.key(projectID, name)]; ok {
		s.Status = status
		s.Endpoint = endpoint
	}
	return nil
}
func (r *fakeServiceRepo) List(_ context.Context, projectID string, _, _ int) ([]*serving.Service, error) {
	r.listCalled = true
	var out []*serving.Service
	for _, s := range r.services {
		if s.ProjectID == projectID {
			out = append(out, s)
		}
	}
	return out, nil
}
func (r *fakeServiceRepo) Count(_ context.Context, projectID string) (int, error) {
	n := 0
	for _, s := range r.services {
		if s.ProjectID == projectID {
			n++
		}
	}
	return n, nil
}
func (r *fakeServiceRepo) Delete(_ context.Context, projectID, name string) error {
	delete(r.services, r.key(projectID, name))
	return nil
}
func (r *fakeServiceRepo) AppendHistory(context.Context, *serving.Service) error { return nil }
func (r *fakeServiceRepo) ListHistory(context.Context, string, int, int) ([]*serving.ServiceHistory, error) {
	return nil, nil
}
func (r *fakeServiceRepo) CountHistory(context.Context, string) (int, error) { return 0, nil }
func (r *fakeServiceRepo) PurgeHistoryBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type fakeServiceDriver struct {
	stopErr error
}

func (d *fakeServiceDriver) ArtifactTarget() artifact.Target { return artifact.TargetLocal }
func (d *fakeServiceDriver) Deploy(context.Context, serving.ModelService, artifact.Resolved, string) (*serving.Service, error) {
	return nil, errors.New("not implemented")
}
func (d *fakeServiceDriver) Stop(context.Context, *serving.Service) error { return d.stopErr }

// ---- run fakes ----

type fakeRunRepo struct {
	runs            map[string]*run.Run
	finalizedStatus map[string]string
}

func newFakeRunRepo(runs ...*run.Run) *fakeRunRepo {
	m := make(map[string]*run.Run, len(runs))
	for _, r := range runs {
		m[r.ID] = r
	}
	return &fakeRunRepo{runs: m, finalizedStatus: map[string]string{}}
}
func (r *fakeRunRepo) Create(context.Context, *run.Run) error                { return nil }
func (r *fakeRunRepo) Get(_ context.Context, _, id string) (*run.Run, error) { return r.runs[id], nil }
func (r *fakeRunRepo) List(_ context.Context, projectID string, _ run.RunFilter) ([]*run.Run, error) {
	var out []*run.Run
	for _, rn := range r.runs {
		if rn.ProjectID == projectID {
			out = append(out, rn)
		}
	}
	return out, nil
}
func (r *fakeRunRepo) Count(context.Context, string, run.RunFilter) (int, error) {
	return len(r.runs), nil
}
func (r *fakeRunRepo) ListExperiments(context.Context, string, run.ExperimentFilter) ([]run.ExperimentSummary, int, error) {
	return nil, 0, nil
}
func (r *fakeRunRepo) UpdateStatus(_ context.Context, _, id, status string, _ *time.Time) error {
	if rn, ok := r.runs[id]; ok {
		rn.Status = status
	}
	return nil
}
func (r *fakeRunRepo) FinalizeStatusCAS(_ context.Context, _, id, to string, _ *time.Time) (bool, error) {
	rn, ok := r.runs[id]
	if !ok {
		return false, nil
	}
	if rn.Status == run.StatusSuccess || rn.Status == run.StatusFailed || rn.Status == run.StatusCanceled {
		return false, nil
	}
	rn.Status = to
	r.finalizedStatus[id] = to
	return true, nil
}
func (r *fakeRunRepo) MarkRunning(context.Context, string, string, time.Time) error { return nil }
func (r *fakeRunRepo) Delete(_ context.Context, _, id string) error {
	delete(r.runs, id)
	return nil
}
func (r *fakeRunRepo) GetLatestSuccessful(context.Context, string, string) (*run.Run, error) {
	return nil, nil
}
func (r *fakeRunRepo) ListTerminalBefore(context.Context, string, time.Time) ([]*run.Run, error) {
	return nil, nil
}
func (r *fakeRunRepo) ExistingIDs(_ context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		if _, ok := r.runs[id]; ok {
			out[id] = true
		}
	}
	return out, nil
}

type fakeStepRepo struct{}

func (fakeStepRepo) Upsert(context.Context, *run.Step) error                   { return nil }
func (fakeStepRepo) UpsertCAS(context.Context, *run.Step) (bool, error)        { return true, nil }
func (fakeStepRepo) List(context.Context, string, string) ([]*run.Step, error) { return nil, nil }
func (fakeStepRepo) ListByRuns(context.Context, string, []string) (map[string][]*run.Step, error) {
	return nil, nil
}
func (fakeStepRepo) DeleteByRun(context.Context, string, string) error { return nil }

type fakeRunDeleter struct {
	deletedIDs []string
}

func (d *fakeRunDeleter) DeleteRun(_ context.Context, _, id string) error {
	d.deletedIDs = append(d.deletedIDs, id)
	return nil
}
func (d *fakeRunDeleter) DeleteRuns(_ context.Context, _ string, ids []string) error {
	d.deletedIDs = append(d.deletedIDs, ids...)
	return nil
}

func newTestRunlifecycleManager(runRepo run.Repository, deleter runlifecycle.RunDeleter) *runlifecycle.Manager {
	q := queue.NewQueue(context.Background(), runRepo, fakeStepRepo{})
	return runlifecycle.New(runlifecycle.Deps{
		RunRepo:         runRepo,
		StepRepo:        fakeStepRepo{},
		RunDeleter:      deleter,
		Queue:           q,
		DeleteArtifacts: func(context.Context, storage.Store, string) error { return nil },
		DeleteWorkspace: func(string, string) error { return nil },
	})
}

// ---- tests ----

func TestCleanupNotebooksDeletesRunningNotebookAndPurgesReleasedVolume(t *testing.T) {
	const projectID = "proj-a"
	nbRepo := newFakeNotebookRepo(&notebook.NotebookServer{
		ProjectID: projectID, Name: "nb1", Status: notebook.StatusRunning, VolumeID: "vol-1",
	})
	volRepo := newFakeVolumeRepo(&notebook.NotebookVolume{
		ProjectID: projectID, ID: "vol-1", Status: notebook.VolumeStatusReleased,
	})
	driver := &fakeNotebookDriver{}
	nbMgr := notebook.New(nbRepo, volRepo, driver)

	d := Deps{Notebooks: nbMgr, NotebookRepo: nbRepo, Volumes: volRepo}
	if err := d.CleanupLiveResources(context.Background(), projectID); err != nil {
		t.Fatalf("CleanupLiveResources() error: %v", err)
	}
	if got, _ := nbRepo.Get(context.Background(), projectID, "nb1"); got != nil {
		t.Fatalf("notebook not deleted: %+v", got)
	}
	if len(volRepo.deletedIDs) != 1 || volRepo.deletedIDs[0] != "vol-1" {
		t.Fatalf("volume not purged: deletedIDs=%v", volRepo.deletedIDs)
	}
}

func TestCleanupNotebooksAbortsOnDriverStopFailure(t *testing.T) {
	const projectID = "proj-a"
	nbRepo := newFakeNotebookRepo(&notebook.NotebookServer{
		ProjectID: projectID, Name: "nb1", Status: notebook.StatusRunning,
	})
	volRepo := newFakeVolumeRepo()
	stopErr := errors.New("driver unavailable")
	nbMgr := notebook.New(nbRepo, volRepo, &fakeNotebookDriver{stopErr: stopErr})
	svcRepo := newFakeServiceRepo(&serving.Service{ProjectID: projectID, Name: "svc1", Status: serving.StatusRunning})
	svcMgr := serving.New(svcRepo, &fakeServiceDriver{})

	d := Deps{Notebooks: nbMgr, NotebookRepo: nbRepo, Volumes: volRepo, Services: svcMgr, ServiceRepo: svcRepo}
	err := d.CleanupLiveResources(context.Background(), projectID)
	if err == nil || !errors.Is(err, stopErr) {
		t.Fatalf("CleanupLiveResources() error = %v, want wrapping %v", err, stopErr)
	}
	if got, _ := nbRepo.Get(context.Background(), projectID, "nb1"); got == nil {
		t.Fatal("notebook was deleted despite driver.Stop failure")
	}
	if svcRepo.listCalled {
		t.Fatal("service cleanup ran after notebook cleanup aborted")
	}
	if got, _ := svcRepo.Get(context.Background(), projectID, "svc1"); got == nil {
		t.Fatal("service was deleted despite cleanup aborting before it ran")
	}
}

func TestCleanupServicesDeletesRunningService(t *testing.T) {
	const projectID = "proj-a"
	svcRepo := newFakeServiceRepo(&serving.Service{ProjectID: projectID, Name: "svc1", Status: serving.StatusRunning})
	svcMgr := serving.New(svcRepo, &fakeServiceDriver{})

	d := Deps{Services: svcMgr, ServiceRepo: svcRepo}
	if err := d.CleanupLiveResources(context.Background(), projectID); err != nil {
		t.Fatalf("CleanupLiveResources() error: %v", err)
	}
	if got, _ := svcRepo.Get(context.Background(), projectID, "svc1"); got != nil {
		t.Fatalf("service not deleted: %+v", got)
	}
}

func TestCleanupServicesAbortsOnDriverStopFailure(t *testing.T) {
	const projectID = "proj-a"
	stopErr := errors.New("driver unavailable")
	svcRepo := newFakeServiceRepo(&serving.Service{ProjectID: projectID, Name: "svc1", Status: serving.StatusRunning})
	svcMgr := serving.New(svcRepo, &fakeServiceDriver{stopErr: stopErr})

	d := Deps{Services: svcMgr, ServiceRepo: svcRepo}
	err := d.CleanupLiveResources(context.Background(), projectID)
	if err == nil || !errors.Is(err, stopErr) {
		t.Fatalf("CleanupLiveResources() error = %v, want wrapping %v", err, stopErr)
	}
	if got, _ := svcRepo.Get(context.Background(), projectID, "svc1"); got == nil {
		t.Fatal("service was deleted despite driver.Stop failure")
	}
}

func TestCleanupRunsCancelsNonTerminalThenDeletesAll(t *testing.T) {
	const projectID = "proj-a"
	runRepo := newFakeRunRepo(
		&run.Run{ID: "run-running", ProjectID: projectID, Status: run.StatusRunning},
		&run.Run{ID: "run-scheduled", ProjectID: projectID, Status: run.StatusScheduled},
		&run.Run{ID: "run-done", ProjectID: projectID, Status: run.StatusSuccess},
	)
	deleter := &fakeRunDeleter{}
	mgr := newTestRunlifecycleManager(runRepo, deleter)

	d := Deps{Runs: mgr, RunRepo: runRepo}
	if err := d.CleanupLiveResources(context.Background(), projectID); err != nil {
		t.Fatalf("CleanupLiveResources() error: %v", err)
	}

	if runRepo.finalizedStatus["run-running"] != run.StatusCanceled {
		t.Fatalf("run-running finalized status = %q, want canceled", runRepo.finalizedStatus["run-running"])
	}
	if runRepo.finalizedStatus["run-scheduled"] != run.StatusCanceled {
		t.Fatalf("run-scheduled finalized status = %q, want canceled", runRepo.finalizedStatus["run-scheduled"])
	}
	if _, ok := runRepo.finalizedStatus["run-done"]; ok {
		t.Fatal("already-terminal run was canceled")
	}

	got := append([]string(nil), deleter.deletedIDs...)
	sort.Strings(got)
	want := []string{"run-done", "run-running", "run-scheduled"}
	if len(got) != len(want) {
		t.Fatalf("deletedIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("deletedIDs = %v, want %v", got, want)
		}
	}
}

func TestCleanupRunsAbortsWhenDeleteFails(t *testing.T) {
	const projectID = "proj-a"
	runRepo := newFakeRunRepo(&run.Run{ID: "run-1", ProjectID: projectID, Status: run.StatusSuccess})
	deleter := &erroringRunDeleter{err: errors.New("db unavailable")}
	mgr := newTestRunlifecycleManager(runRepo, deleter)

	d := Deps{Runs: mgr, RunRepo: runRepo}
	err := d.CleanupLiveResources(context.Background(), projectID)
	if err == nil {
		t.Fatal("CleanupLiveResources() error = nil, want error")
	}
}

type erroringRunDeleter struct{ err error }

func (d *erroringRunDeleter) DeleteRun(context.Context, string, string) error    { return d.err }
func (d *erroringRunDeleter) DeleteRuns(context.Context, string, []string) error { return d.err }

func TestCleanupLiveResourcesNoopWhenDepsUnset(t *testing.T) {
	d := Deps{}
	if err := d.CleanupLiveResources(context.Background(), "proj-a"); err != nil {
		t.Fatalf("CleanupLiveResources() error = %v, want nil for empty Deps", err)
	}
}

// ---- template fakes ----

type fakeTemplateRepo struct {
	byProject map[string][]*template.Template
	listErr   error
}

func (r *fakeTemplateRepo) NextVersion(context.Context, string, string) (int, error) { return 1, nil }
func (r *fakeTemplateRepo) Create(context.Context, *template.Template) error         { return nil }
func (r *fakeTemplateRepo) Get(context.Context, string, string) (*template.Template, error) {
	return nil, nil
}
func (r *fakeTemplateRepo) Count(_ context.Context, projectID string, _ template.Filter) (int, error) {
	return len(r.byProject[projectID]), nil
}
func (r *fakeTemplateRepo) Delete(context.Context, string, string) error { return nil }

func (r *fakeTemplateRepo) List(_ context.Context, projectID string, f template.Filter) ([]*template.Template, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	all := r.byProject[projectID]
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	start := f.Offset
	if start >= len(all) {
		return nil, nil
	}
	end := start + limit
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], nil
}

func TestCleanupTemplateSnapshotsDeletesSnapshotObjects(t *testing.T) {
	const projectID = "proj-a"
	store := storage.NewMemStore()
	if err := store.Put(context.Background(), "snapshots/snap-1/main.py", strings.NewReader("print(1)"), -1); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "snapshots/snap-1/util.py", strings.NewReader("x=1"), -1); err != nil {
		t.Fatal(err)
	}
	repo := &fakeTemplateRepo{byProject: map[string][]*template.Template{
		projectID: {{ID: "t1", ProjectID: projectID, SnapshotID: "snap-1"}},
	}}

	d := Deps{Templates: repo, Store: store}
	d.cleanupTemplateSnapshots(context.Background(), projectID)

	objs, err := store.List(context.Background(), "snapshots/snap-1/", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 0 {
		t.Fatalf("snapshot objects still present after cleanup: %#v", objs)
	}
}

func TestCleanupTemplateSnapshotsPaginatesPastFirstPage(t *testing.T) {
	const projectID = "proj-a"
	store := storage.NewMemStore()
	templates := make([]*template.Template, 0, 60)
	for i := 0; i < 60; i++ {
		snapID := "snap-" + strconv.Itoa(i)
		if err := store.Put(context.Background(), "snapshots/"+snapID+"/main.py", strings.NewReader("x"), -1); err != nil {
			t.Fatal(err)
		}
		templates = append(templates, &template.Template{ID: "t" + strconv.Itoa(i), ProjectID: projectID, SnapshotID: snapID})
	}
	repo := &fakeTemplateRepo{byProject: map[string][]*template.Template{projectID: templates}}

	d := Deps{Templates: repo, Store: store}
	d.cleanupTemplateSnapshots(context.Background(), projectID)

	objs, err := store.List(context.Background(), "snapshots/snap-59/", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 0 {
		t.Fatalf("last-page snapshot object still present after cleanup: %#v", objs)
	}
}

func TestCleanupTemplateSnapshotsNoopWhenStoreUnset(t *testing.T) {
	d := Deps{Templates: &fakeTemplateRepo{}}
	d.cleanupTemplateSnapshots(context.Background(), "proj-a") // must not panic
}
