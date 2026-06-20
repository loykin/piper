package notebook

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ─── In-memory fakes ──────────────────────────────────────────────────────────

type fakeRepo struct {
	mu      sync.Mutex
	servers map[string]*NotebookServer
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{servers: make(map[string]*NotebookServer)}
}

func (r *fakeRepo) Create(_ context.Context, nb *NotebookServer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *nb
	r.servers[nb.Name] = &cp
	return nil
}

func (r *fakeRepo) Get(_ context.Context, _, name string) (*NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	nb, ok := r.servers[name]
	if !ok {
		return nil, nil
	}
	cp := *nb
	return &cp, nil
}

func (r *fakeRepo) Update(_ context.Context, nb *NotebookServer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.servers[nb.Name]; !ok {
		return errors.New("not found")
	}
	cp := *nb
	r.servers[nb.Name] = &cp
	return nil
}

func (r *fakeRepo) SetStatus(_ context.Context, _, name, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	nb, ok := r.servers[name]
	if !ok {
		return errors.New("not found")
	}
	nb.Status = status
	return nil
}

func (r *fakeRepo) GetByVolumeID(_ context.Context, _, volumeID string) (*NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, nb := range r.servers {
		if nb.VolumeID == volumeID {
			cp := *nb
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) List(_ context.Context, _ string) ([]*NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*NotebookServer, 0, len(r.servers))
	for _, nb := range r.servers {
		cp := *nb
		out = append(out, &cp)
	}
	return out, nil
}

func (r *fakeRepo) ListByWorker(_ context.Context, workerID string) ([]*NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*NotebookServer
	for _, nb := range r.servers {
		if nb.WorkerID == workerID {
			cp := *nb
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeRepo) Delete(_ context.Context, _, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.servers, name)
	return nil
}

// helper: read without lock (call only from same test goroutine after sync point)
func (r *fakeRepo) get(name string) *NotebookServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	nb, ok := r.servers[name]
	if !ok {
		return nil
	}
	cp := *nb
	return &cp
}

// ─── fake VolumeRepository ────────────────────────────────────────────────────

type fakeVols struct {
	mu   sync.Mutex
	vols map[string]*NotebookVolume
}

func newFakeVols() *fakeVols {
	return &fakeVols{vols: make(map[string]*NotebookVolume)}
}

func (v *fakeVols) Create(_ context.Context, vol *NotebookVolume) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	cp := *vol
	v.vols[vol.ID] = &cp
	return nil
}

func (v *fakeVols) Get(_ context.Context, id string) (*NotebookVolume, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	vol, ok := v.vols[id]
	if !ok {
		return nil, nil
	}
	cp := *vol
	return &cp, nil
}

func (v *fakeVols) List(_ context.Context, _ string) ([]*NotebookVolume, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]*NotebookVolume, 0, len(v.vols))
	for _, vol := range v.vols {
		cp := *vol
		out = append(out, &cp)
	}
	return out, nil
}

func (v *fakeVols) Update(_ context.Context, vol *NotebookVolume) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.vols[vol.ID]; !ok {
		return errors.New("not found")
	}
	cp := *vol
	v.vols[vol.ID] = &cp
	return nil
}

func (v *fakeVols) SetStatus(_ context.Context, id, status string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	vol, ok := v.vols[id]
	if !ok {
		return errors.New("not found")
	}
	vol.Status = status
	return nil
}

func (v *fakeVols) Delete(_ context.Context, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.vols, id)
	return nil
}

func (v *fakeVols) get(id string) *NotebookVolume {
	v.mu.Lock()
	defer v.mu.Unlock()
	vol, ok := v.vols[id]
	if !ok {
		return nil
	}
	cp := *vol
	return &cp
}

func requireNotebook(t *testing.T, repo *fakeRepo, name string) *NotebookServer {
	t.Helper()
	nb := repo.get(name)
	if nb == nil {
		t.Fatalf("notebook %q should exist", name)
	}
	return nb
}

func requireVolume(t *testing.T, vols *fakeVols, id string) *NotebookVolume {
	t.Helper()
	vol := vols.get(id)
	if vol == nil {
		t.Fatalf("volume %q should exist", id)
	}
	return vol
}

// ─── fake Driver ──────────────────────────────────────────────────────────────

type fakeDriver struct {
	provisionErr  error
	provisionWork func(*NotebookVolume) // optional side-effect (e.g. set WorkDir)
	startErr      error
	startResult   *NotebookServer // returned by Start (nil → empty &NotebookServer{})
	stopErr       error
	deprovErr     error

	provisionDone chan struct{} // closed when ProvisionVolume returns
	startDone     chan struct{} // closed when Start returns
	stopCalled    chan struct{} // closed when Stop is called
	deprovCalled  chan struct{} // closed when DeprovisionVolume is called
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{
		provisionDone: make(chan struct{}, 1),
		startDone:     make(chan struct{}, 1),
		stopCalled:    make(chan struct{}, 1),
		deprovCalled:  make(chan struct{}, 1),
	}
}

func (d *fakeDriver) ProvisionVolume(_ context.Context, vol *NotebookVolume, _ Notebook) error {
	if d.provisionWork != nil {
		d.provisionWork(vol)
	}
	select {
	case d.provisionDone <- struct{}{}:
	default:
	}
	return d.provisionErr
}

func (d *fakeDriver) Start(_ context.Context, _ Notebook, _ *NotebookVolume, _ string) (*NotebookServer, error) {
	select {
	case d.startDone <- struct{}{}:
	default:
	}
	if d.startErr != nil {
		return nil, d.startErr
	}
	if d.startResult != nil {
		return d.startResult, nil
	}
	return &NotebookServer{}, nil
}

func (d *fakeDriver) Stop(_ context.Context, _ *NotebookServer) error {
	select {
	case d.stopCalled <- struct{}{}:
	default:
	}
	return d.stopErr
}

func (d *fakeDriver) DeprovisionVolume(_ context.Context, _ *NotebookVolume) error {
	select {
	case d.deprovCalled <- struct{}{}:
	default:
	}
	return d.deprovErr
}

// waitStart blocks until Start has been called or the timeout expires.
func (d *fakeDriver) waitStart(t *testing.T) {
	t.Helper()
	select {
	case <-d.startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fakeDriver.Start was not called within 2s")
	}
}

// waitProvision blocks until ProvisionVolume has been called or the timeout expires.
func (d *fakeDriver) waitProvision(t *testing.T) {
	t.Helper()
	select {
	case <-d.provisionDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fakeDriver.ProvisionVolume was not called within 2s")
	}
}

// ─── Create tests ─────────────────────────────────────────────────────────────

func TestManager_Create_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	drv.provisionWork = func(vol *NotebookVolume) { vol.WorkDir = "/work/vol1"; vol.WorkerID = "w-1" }
	drv.startResult = &NotebookServer{WorkerID: "w-1", Token: "tok-abc", WorkDir: "/work/vol1"}

	m := New(repo, vols, drv)
	spec := Notebook{}
	spec.Metadata.Name = "nb-create"

	nb, err := m.Create(context.Background(), "project-a", spec, "")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if nb.Status != StatusProvisioning {
		t.Errorf("Create() initial status = %q, want %q", nb.Status, StatusProvisioning)
	}
	if nb.VolumeID == "" {
		t.Error("Create() VolumeID is empty")
	}

	// Wait for background goroutine to complete both phases.
	drv.waitProvision(t)
	drv.waitStart(t)

	// Allow goroutine to finish writing back.
	time.Sleep(50 * time.Millisecond)

	stored := repo.get("nb-create")
	if stored == nil {
		t.Fatal("server record not found after Create()")
	}
	if stored.WorkerID != "w-1" {
		t.Errorf("WorkerID = %q, want %q", stored.WorkerID, "w-1")
	}
	if stored.Token != "tok-abc" {
		t.Errorf("Token = %q, want %q", stored.Token, "tok-abc")
	}

	vol := vols.get(nb.VolumeID)
	if vol == nil {
		t.Fatal("volume record not found after Create()")
	}
	if vol.WorkDir != "/work/vol1" {
		t.Errorf("vol.WorkDir = %q, want %q", vol.WorkDir, "/work/vol1")
	}
}

func TestManager_Create_DuplicateName(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	m := New(repo, vols, drv)

	spec := Notebook{}
	spec.Metadata.Name = "dup"

	if _, err := m.Create(context.Background(), "project-a", spec, ""); err != nil {
		t.Fatalf("first Create() error: %v", err)
	}
	if _, err := m.Create(context.Background(), "project-a", spec, ""); err == nil {
		t.Fatal("second Create() expected error, got nil")
	}
}

func TestManager_Create_EmptyName(t *testing.T) {
	m := New(newFakeRepo(), newFakeVols(), newFakeDriver())
	_, err := m.Create(context.Background(), "project-a", Notebook{}, "")
	if err == nil {
		t.Fatal("Create() with empty name expected error")
	}
}

func TestManager_Create_InvalidPrepareSpec(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	m := New(repo, vols, drv)

	spec := Notebook{}
	spec.Metadata.Name = "nb-prepare-invalid"
	spec.Spec.Prepare = &NotebookPrepareSpec{
		Steps: []NotebookPrepareStep{
			{Type: "unknown"},
		},
	}

	_, err := m.Create(context.Background(), "project-a", spec, "")
	if err == nil {
		t.Fatal("Create() with invalid prepare spec expected error")
	}
}

func TestManager_Create_ProvisionFails(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	drv.provisionErr = errors.New("disk full")

	m := New(repo, vols, drv)
	spec := Notebook{}
	spec.Metadata.Name = "nb-prov-fail"

	nb, err := m.Create(context.Background(), "project-a", spec, "")
	if err != nil {
		t.Fatalf("Create() itself should not error: %v", err)
	}

	// Wait for background to run ProvisionVolume.
	drv.waitProvision(t)
	time.Sleep(50 * time.Millisecond)

	stored := repo.get(nb.Name)
	if stored != nil && stored.Status != StatusFailed {
		t.Errorf("status = %q, want %q", stored.Status, StatusFailed)
	}
	// Volume should be cleaned up.
	if vols.get(nb.VolumeID) != nil {
		t.Error("volume should be deleted after provision failure")
	}
}

func TestManager_Create_StartFails(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	drv.provisionWork = func(vol *NotebookVolume) { vol.WorkDir = "/work/fail-start" }
	drv.startErr = errors.New("no worker available")

	m := New(repo, vols, drv)
	spec := Notebook{}
	spec.Metadata.Name = "nb-start-fail"

	nb, err := m.Create(context.Background(), "project-a", spec, "")
	if err != nil {
		t.Fatalf("Create() itself should not error: %v", err)
	}

	drv.waitProvision(t)
	drv.waitStart(t)
	time.Sleep(50 * time.Millisecond)

	stored := repo.get(nb.Name)
	if stored != nil && stored.Status != StatusFailed {
		t.Errorf("status = %q, want %q", stored.Status, StatusFailed)
	}
	// Volume should be cleaned up on start failure.
	if vols.get(nb.VolumeID) != nil {
		t.Error("volume should be deleted after start failure")
	}
}

// ─── CreateWithVolume tests ───────────────────────────────────────────────────

func TestManager_CreateWithVolume_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	drv.startResult = &NotebookServer{WorkerID: "w-2", Token: "tok-vol"}

	m := New(repo, vols, drv)
	ctx := context.Background()

	vol := &NotebookVolume{ProjectID: "project-a", ID: "vol-existing", Label: "old-nb", WorkDir: "/data/old-nb", Status: VolumeStatusReleased}
	_ = vols.Create(ctx, vol)

	spec := Notebook{}
	spec.Metadata.Name = "nb-attach"

	nb, err := m.CreateWithVolume(ctx, "project-a", spec, "vol-existing", "")
	if err != nil {
		t.Fatalf("CreateWithVolume() error: %v", err)
	}
	if nb.Status != StatusStarting {
		t.Errorf("status = %q, want %q", nb.Status, StatusStarting)
	}
	if nb.VolumeID != "vol-existing" {
		t.Errorf("VolumeID = %q, want %q", nb.VolumeID, "vol-existing")
	}

	drv.waitStart(t)
	time.Sleep(50 * time.Millisecond)

	// Volume should be bound.
	v := vols.get("vol-existing")
	if v == nil {
		t.Fatal("volume not found")
	}
	if v.Status != VolumeStatusBound {
		t.Errorf("vol.Status = %q, want %q", v.Status, VolumeStatusBound)
	}
}

func TestManager_CreateWithVolume_VolumeNotFound(t *testing.T) {
	m := New(newFakeRepo(), newFakeVols(), newFakeDriver())
	spec := Notebook{}
	spec.Metadata.Name = "nb-no-vol"
	_, err := m.CreateWithVolume(context.Background(), "project-a", spec, "missing-vol", "")
	if err == nil {
		t.Fatal("expected error for missing volume")
	}
}

func TestManager_CreateWithVolume_VolumeNotReleased(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	m := New(repo, vols, newFakeDriver())
	ctx := context.Background()

	vol := &NotebookVolume{ProjectID: "project-a", ID: "vol-bound", Label: "nb", Status: VolumeStatusBound}
	_ = vols.Create(ctx, vol)

	spec := Notebook{}
	spec.Metadata.Name = "nb-reuse"
	_, err := m.CreateWithVolume(ctx, "project-a", spec, "vol-bound", "")
	if err == nil {
		t.Fatal("expected error for non-released volume")
	}
}

func TestManager_CreateWithVolume_StartFails(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	drv.startErr = errors.New("worker down")

	m := New(repo, vols, drv)
	ctx := context.Background()

	vol := &NotebookVolume{ProjectID: "project-a", ID: "vol-sfail", Label: "sfail", Status: VolumeStatusReleased}
	_ = vols.Create(ctx, vol)

	spec := Notebook{}
	spec.Metadata.Name = "nb-sfail"
	nb, err := m.CreateWithVolume(ctx, "project-a", spec, "vol-sfail", "")
	if err != nil {
		t.Fatalf("CreateWithVolume() should return immediately: %v", err)
	}

	drv.waitStart(t)
	time.Sleep(50 * time.Millisecond)

	stored := repo.get(nb.Name)
	if stored != nil && stored.Status != StatusFailed {
		t.Errorf("status = %q, want %q", stored.Status, StatusFailed)
	}
	// Volume should revert to released.
	v := vols.get("vol-sfail")
	if v == nil {
		t.Fatal("volume should still exist")
	}
	if v.Status != VolumeStatusReleased {
		t.Errorf("vol.Status = %q, want %q", v.Status, VolumeStatusReleased)
	}
}

// ─── Stop tests ───────────────────────────────────────────────────────────────

func TestManager_Stop_RunningServer(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	m := New(repo, vols, drv)
	ctx := context.Background()

	_ = repo.Create(ctx, &NotebookServer{Name: "nb-stop", Status: StatusRunning})

	if err := m.Stop(ctx, "project-a", "nb-stop"); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	select {
	case <-drv.stopCalled:
	case <-time.After(time.Second):
		t.Error("driver.Stop not called")
	}

	// Stop RPC delivered successfully: status is "stopping" until the worker
	// pushes the final "stopped" status via UpdateStatus.
	nb := repo.get("nb-stop")
	if nb.Status != StatusStopping {
		t.Errorf("status = %q, want %q", nb.Status, StatusStopping)
	}
}

func TestManager_Stop_DriverFailureRestoresObservedStatus(t *testing.T) {
	repo := newFakeRepo()
	drv := newFakeDriver()
	drv.stopErr = ErrAgentUnavailable
	m := New(repo, newFakeVols(), drv)
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-offline", Status: StatusRunning})

	if err := m.Stop(ctx, "project-a", "nb-offline"); !errors.Is(err, ErrAgentUnavailable) {
		t.Fatalf("Stop() error = %v, want ErrAgentUnavailable", err)
	}
	nb := repo.get("nb-offline")
	if nb == nil {
		t.Fatal("notebook not found")
	}
	if nb.Status != StatusRunning {
		t.Fatalf("status = %q, want last observed status %q", nb.Status, StatusRunning)
	}
}

func TestManager_Stop_AlreadyStopped(t *testing.T) {
	repo := newFakeRepo()
	m := New(repo, newFakeVols(), newFakeDriver())
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-already-stopped", Status: StatusStopped})

	if err := m.Stop(ctx, "project-a", "nb-already-stopped"); err != nil {
		t.Fatalf("Stop() on stopped server should be no-op: %v", err)
	}
}

func TestManager_Stop_NotFound(t *testing.T) {
	m := New(newFakeRepo(), newFakeVols(), newFakeDriver())
	if err := m.Stop(context.Background(), "project-a", "no-such"); err == nil {
		t.Fatal("Stop() on missing server expected error")
	}
}

// ─── Restart tests ────────────────────────────────────────────────────────────

func TestManager_Restart_StoppedServer(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	drv.startResult = &NotebookServer{WorkerID: "w-rst", Token: "tok-rst"}

	m := New(repo, vols, drv)
	ctx := context.Background()

	vol := &NotebookVolume{ID: "vol-rst", Label: "nb-rst", WorkDir: "/data/rst", Status: VolumeStatusBound}
	_ = vols.Create(ctx, vol)
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-rst", Status: StatusStopped, VolumeID: "vol-rst", WorkDir: "/data/rst"})

	if err := m.Restart(ctx, "project-a", "nb-rst"); err != nil {
		t.Fatalf("Restart() error: %v", err)
	}

	// Status should be "starting" immediately.
	nb := requireNotebook(t, repo, "nb-rst")
	if nb.Status != StatusStarting {
		t.Errorf("status after Restart() = %q, want %q", nb.Status, StatusStarting)
	}

	drv.waitStart(t)
	time.Sleep(50 * time.Millisecond)

	nb = requireNotebook(t, repo, "nb-rst")
	if nb.WorkerID != "w-rst" {
		t.Errorf("WorkerID = %q, want %q", nb.WorkerID, "w-rst")
	}
}

func TestManager_Restart_AlreadyRunning(t *testing.T) {
	repo := newFakeRepo()
	m := New(repo, newFakeVols(), newFakeDriver())
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-running", Status: StatusRunning})

	if err := m.Restart(ctx, "project-a", "nb-running"); err != nil {
		t.Fatalf("Restart() on running server should be no-op: %v", err)
	}
}

func TestManager_Restart_NoVolumeNoWorkDir(t *testing.T) {
	repo := newFakeRepo()
	m := New(repo, newFakeVols(), newFakeDriver())
	ctx := context.Background()
	// Server with empty VolumeID and empty WorkDir — cannot restart.
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-nodir", Status: StatusStopped})

	if err := m.Restart(ctx, "project-a", "nb-nodir"); err == nil {
		t.Fatal("Restart() with no volume and no work_dir expected error")
	}
}

// ─── Delete tests ─────────────────────────────────────────────────────────────

func TestManager_Delete_StoppedServer(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	m := New(repo, vols, drv)
	ctx := context.Background()

	vol := &NotebookVolume{ID: "vol-del", Label: "nb-del", Status: VolumeStatusBound}
	_ = vols.Create(ctx, vol)
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-del", Status: StatusStopped, VolumeID: "vol-del"})

	if err := m.Delete(ctx, "project-a", "nb-del"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if repo.get("nb-del") != nil {
		t.Error("server record should be deleted")
	}
	v := vols.get("vol-del")
	if v == nil {
		t.Fatal("volume should still exist after delete")
	}
	if v.Status != VolumeStatusReleased {
		t.Errorf("vol.Status = %q, want %q", v.Status, VolumeStatusReleased)
	}
}

func TestManager_Delete_RunningServerCallsStop(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	m := New(repo, vols, drv)
	ctx := context.Background()

	vol := &NotebookVolume{ID: "vol-del-run", Label: "nb-del-run", Status: VolumeStatusBound}
	_ = vols.Create(ctx, vol)
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-del-run", Status: StatusRunning, VolumeID: "vol-del-run"})

	if err := m.Delete(ctx, "project-a", "nb-del-run"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	select {
	case <-drv.stopCalled:
	case <-time.After(time.Second):
		t.Error("driver.Stop should be called when deleting a running server")
	}
}

func TestManager_Delete_NotFound(t *testing.T) {
	m := New(newFakeRepo(), newFakeVols(), newFakeDriver())
	if err := m.Delete(context.Background(), "project-a", "ghost"); err == nil {
		t.Fatal("Delete() on missing server expected error")
	}
}

// ─── PurgeVolume tests ────────────────────────────────────────────────────────

func TestManager_PurgeVolume_ReleasedVolume(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	drv := newFakeDriver()
	m := New(repo, vols, drv)
	ctx := context.Background()

	vol := &NotebookVolume{ProjectID: "project-a", ID: "vol-purge", Label: "old", WorkDir: "/data/old", Status: VolumeStatusReleased}
	_ = vols.Create(ctx, vol)

	if err := m.PurgeVolume(ctx, "project-a", "vol-purge"); err != nil {
		t.Fatalf("PurgeVolume() error: %v", err)
	}

	select {
	case <-drv.deprovCalled:
	case <-time.After(time.Second):
		t.Error("driver.DeprovisionVolume should be called")
	}
	if vols.get("vol-purge") != nil {
		t.Error("volume record should be deleted after purge")
	}
}

func TestManager_PurgeVolume_BoundVolume(t *testing.T) {
	vols := newFakeVols()
	m := New(newFakeRepo(), vols, newFakeDriver())
	ctx := context.Background()

	vol := &NotebookVolume{ID: "vol-bound-purge", Status: VolumeStatusBound}
	_ = vols.Create(ctx, vol)

	if err := m.PurgeVolume(ctx, "project-a", "vol-bound-purge"); err == nil {
		t.Fatal("PurgeVolume() on bound volume expected error")
	}
}

func TestManager_PurgeVolume_NotFound(t *testing.T) {
	m := New(newFakeRepo(), newFakeVols(), newFakeDriver())
	if err := m.PurgeVolume(context.Background(), "project-a", "no-vol"); err == nil {
		t.Fatal("PurgeVolume() on missing volume expected error")
	}
}

// ─── UpdateStatus tests ───────────────────────────────────────────────────────

func TestManager_UpdateStatus_FullUpdate(t *testing.T) {
	repo := newFakeRepo()
	vols := newFakeVols()
	m := New(repo, vols, newFakeDriver())
	ctx := context.Background()

	vol := &NotebookVolume{ID: "vol-us", Label: "nb-us", WorkDir: "/old", Status: VolumeStatusBound}
	_ = vols.Create(ctx, vol)
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-us", Status: StatusStarting, VolumeID: "vol-us"})

	err := m.UpdateStatus(ctx, "project-a", "", "nb-us", StatusRunning, "http://worker:8888", "/new/workdir", "new-token", 42, "base")
	if err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	nb := requireNotebook(t, repo, "nb-us")
	if nb.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", nb.Status, StatusRunning)
	}
	if nb.Endpoint != "http://worker:8888" {
		t.Errorf("Endpoint = %q", nb.Endpoint)
	}
	if nb.Token != "new-token" {
		t.Errorf("Token = %q", nb.Token)
	}
	if nb.PID != 42 {
		t.Errorf("PID = %d, want 42", nb.PID)
	}
	if nb.Env != "base" {
		t.Errorf("Env = %q", nb.Env)
	}

	// Volume's WorkDir should be updated too.
	v := requireVolume(t, vols, "vol-us")
	if v.WorkDir != "/new/workdir" {
		t.Errorf("vol.WorkDir = %q, want %q", v.WorkDir, "/new/workdir")
	}
}

func TestManager_UpdateStatus_NotFound(t *testing.T) {
	m := New(newFakeRepo(), newFakeVols(), newFakeDriver())
	if err := m.UpdateStatus(context.Background(), "project-a", "", "ghost", StatusRunning, "", "", "", 0, ""); err == nil {
		t.Fatal("UpdateStatus() on missing server expected error")
	}
}

func TestManagerUpdateStatusRejectsDifferentWorker(t *testing.T) {
	repo := newFakeRepo()
	m := New(repo, newFakeVols(), newFakeDriver())
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-owned", WorkerID: "worker-a", Status: StatusRunning})

	err := m.UpdateStatus(ctx, "project-a", "worker-b", "nb-owned", StatusStopped, "", "", "", 0, "")
	if err == nil {
		t.Fatal("UpdateStatus() accepted update from non-owner")
	}
	nbOwned := repo.get("nb-owned")
	if nbOwned == nil {
		t.Fatal("notebook not found")
	}
	if nbOwned.Status != StatusRunning {
		t.Fatalf("status = %q, want unchanged %q", nbOwned.Status, StatusRunning)
	}
}

func TestManagerUpdateStatusClearsRuntimeFieldsWhenStopped(t *testing.T) {
	repo := newFakeRepo()
	m := New(repo, newFakeVols(), newFakeDriver())
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{
		Name:     "nb-stale-runtime",
		Status:   StatusRunning,
		Endpoint: "tunnel://worker-a?target=127.0.0.1:8888",
		PID:      123,
		Token:    "secret",
	})

	if err := m.UpdateStatus(ctx, "project-a", "", "nb-stale-runtime", StatusStopped, "", "", "", 0, ""); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}
	nb := repo.get("nb-stale-runtime")
	if nb == nil {
		t.Fatal("notebook not found")
	}
	if nb.Endpoint != "" || nb.PID != 0 || nb.Token != "" {
		t.Fatalf("runtime fields not cleared: endpoint=%q pid=%d token=%q", nb.Endpoint, nb.PID, nb.Token)
	}
}

func TestManager_UpdateStatus_PartialUpdate(t *testing.T) {
	repo := newFakeRepo()
	m := New(repo, newFakeVols(), newFakeDriver())
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb-partial", Status: StatusStarting, Token: "keep-me", PID: 0})

	// Only update status; other fields should be preserved.
	if err := m.UpdateStatus(ctx, "project-a", "", "nb-partial", StatusRunning, "", "", "", 0, ""); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	nb := requireNotebook(t, repo, "nb-partial")
	if nb.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", nb.Status, StatusRunning)
	}
	if nb.Token != "keep-me" {
		t.Errorf("Token was overwritten: %q", nb.Token)
	}
}
