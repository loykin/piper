package notebook

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/piper/piper/internal/event"
)

// Manager handles the lifecycle of NotebookServer instances.
// It delegates process and volume management to a Driver.
type Manager struct {
	repo   Repository
	vols   VolumeRepository
	driver Driver
	events event.Publisher
}

// New creates a Manager backed by the given repositories and driver.
func New(repo Repository, vols VolumeRepository, driver Driver) *Manager {
	return &Manager{repo: repo, vols: vols, driver: driver}
}

// SetEventPublisher wires an event publisher for notebook lifecycle events.
func (m *Manager) SetEventPublisher(p event.Publisher) {
	m.events = p
}

// Create provisions new storage and launches a new notebook server asynchronously.
// Returns immediately with status=provisioning; background goroutine handles
// volume allocation and server start. Worker callback sets final status.
func (m *Manager) Create(ctx context.Context, projectID string, spec Notebook, yamlStr string) (*NotebookServer, error) {
	if projectID == "" {
		return nil, fmt.Errorf("notebook: project ID is required")
	}
	spec.Metadata.ProjectID = projectID
	name := spec.Metadata.Name
	if name == "" {
		return nil, fmt.Errorf("notebook: metadata.name is required")
	}
	if err := spec.Spec.Prepare.Validate(); err != nil {
		return nil, fmt.Errorf("notebook: invalid prepare spec: %w", err)
	}
	if existing, _ := m.repo.Get(ctx, projectID, name); existing != nil {
		return nil, fmt.Errorf("notebook %q already exists (status: %s)", name, existing.Status)
	}

	vol := &NotebookVolume{
		ProjectID: projectID,
		ID:        uuid.NewString(),
		Label:     name,
		Status:    VolumeStatusBound,
	}
	if err := m.vols.Create(ctx, vol); err != nil {
		return nil, fmt.Errorf("notebook: create volume: %w", err)
	}

	nb := &NotebookServer{
		ProjectID: projectID,
		Name:      name,
		Status:    StatusProvisioning,
		VolumeID:  vol.ID,
		YAML:      yamlStr,
	}
	if err := m.repo.Create(ctx, nb); err != nil {
		_ = m.vols.Delete(ctx, vol.ID)
		return nil, fmt.Errorf("notebook: create server record: %w", err)
	}

	// Detach from request context so background work continues after HTTP response.
	bgCtx := context.WithoutCancel(ctx)
	go m.provisionAndStart(bgCtx, projectID, vol, spec, yamlStr)

	m.emit(projectID, "notebook.creating", map[string]any{"name": name})
	return nb, nil
}

// provisionAndStart handles the async two-phase startup: volume provisioning then server start.
// Status transitions: provisioning → starting → (running or failed via worker callback).
func (m *Manager) provisionAndStart(ctx context.Context, projectID string, vol *NotebookVolume, spec Notebook, yamlStr string) {
	spec.Metadata.ProjectID = projectID
	name := spec.Metadata.Name

	// Phase 1: provision storage
	if err := m.driver.ProvisionVolume(ctx, vol, spec.StorageSize()); err != nil {
		slog.Error("notebook: provision volume failed", "name", name, "err", err)
		_ = m.repo.SetStatus(ctx, projectID, name, StatusFailed)
		_ = m.vols.Delete(ctx, vol.ID)
		m.emit(projectID, "notebook.failed", map[string]any{"name": name, "error": err.Error()})
		return
	}
	if err := m.vols.Update(ctx, vol); err != nil {
		slog.Warn("notebook: update volume after provision failed", "name", name, "err", err)
	}
	_ = m.repo.SetStatus(ctx, projectID, name, StatusStarting)

	// Phase 2: start the server process/pod (driver.Start returns quickly; actual
	// startup is async on the worker, which will call back with status=running).
	fresh, err := m.driver.Start(ctx, spec, vol, yamlStr)
	if err != nil {
		slog.Error("notebook: start failed", "name", name, "err", err)
		_ = m.repo.SetStatus(ctx, projectID, name, StatusFailed)
		_ = m.driver.DeprovisionVolume(ctx, vol)
		_ = m.vols.Delete(ctx, vol.ID)
		m.emit(projectID, "notebook.failed", map[string]any{"name": name, "error": err.Error()})
		return
	}

	// Persist worker assignment, endpoint, image, and initial token from Start response.
	nb, err := m.repo.Get(ctx, projectID, name)
	if err != nil {
		slog.Error("notebook: get record after start failed", "name", name, "err", err)
		return
	}
	if nb != nil {
		if fresh.WorkerID != "" {
			nb.WorkerID = fresh.WorkerID
		}
		if fresh.Token != "" {
			nb.Token = fresh.Token
		}
		if fresh.WorkDir != "" {
			nb.WorkDir = fresh.WorkDir
		}
		if fresh.Endpoint != "" {
			nb.Endpoint = fresh.Endpoint
		}
		if fresh.Image != "" {
			nb.Image = fresh.Image
		}
		// Keep status=starting; worker callback will set it to running/failed.
		_ = m.repo.Update(ctx, nb)
	}
}

// CreateWithVolume launches a new notebook server backed by an existing released volume.
// Returns immediately with status=starting; background goroutine handles server start.
func (m *Manager) CreateWithVolume(ctx context.Context, projectID string, spec Notebook, volumeID string, yamlStr string) (*NotebookServer, error) {
	if projectID == "" {
		return nil, fmt.Errorf("notebook: project ID is required")
	}
	spec.Metadata.ProjectID = projectID
	name := spec.Metadata.Name
	if name == "" {
		return nil, fmt.Errorf("notebook: metadata.name is required")
	}
	if err := spec.Spec.Prepare.Validate(); err != nil {
		return nil, fmt.Errorf("notebook: invalid prepare spec: %w", err)
	}
	vol, err := m.vols.Get(ctx, volumeID)
	if err != nil || vol == nil {
		return nil, fmt.Errorf("notebook: volume %q not found", volumeID)
	}
	if vol.ProjectID != projectID {
		return nil, fmt.Errorf("notebook: volume %q not found", volumeID)
	}
	if vol.Status != VolumeStatusReleased {
		return nil, fmt.Errorf("notebook: volume %q is not released (status: %s)", volumeID, vol.Status)
	}
	if existing, _ := m.repo.Get(ctx, projectID, name); existing != nil {
		return nil, fmt.Errorf("notebook %q already exists (status: %s)", name, existing.Status)
	}

	if err := m.vols.SetStatus(ctx, vol.ID, VolumeStatusBound); err != nil {
		return nil, fmt.Errorf("notebook: bind volume: %w", err)
	}

	// Volume is already provisioned, skip straight to starting.
	nb := &NotebookServer{
		ProjectID: projectID,
		Name:      name,
		Status:    StatusStarting,
		VolumeID:  vol.ID,
		YAML:      yamlStr,
	}
	if err := m.repo.Create(ctx, nb); err != nil {
		_ = m.vols.SetStatus(ctx, vol.ID, VolumeStatusReleased)
		return nil, fmt.Errorf("notebook: create server record: %w", err)
	}

	bgCtx := context.WithoutCancel(ctx)
	volCopy := *vol
	go func() {
		fresh, err := m.driver.Start(bgCtx, spec, &volCopy, yamlStr)
		if err != nil {
			slog.Error("notebook: start with volume failed", "name", name, "err", err)
			_ = m.repo.SetStatus(bgCtx, projectID, name, StatusFailed)
			_ = m.vols.SetStatus(bgCtx, vol.ID, VolumeStatusReleased)
			m.emit(projectID, "notebook.failed", map[string]any{"name": name, "error": err.Error()})
			return
		}
		if nb, err := m.repo.Get(bgCtx, projectID, name); err == nil && nb != nil {
			volCopy.Status = VolumeStatusBound
			if fresh.WorkerID != "" {
				nb.WorkerID = fresh.WorkerID
				volCopy.WorkerID = fresh.WorkerID
			}
			if fresh.Token != "" {
				nb.Token = fresh.Token
			}
			if fresh.WorkDir != "" {
				nb.WorkDir = fresh.WorkDir
			}
			if fresh.Endpoint != "" {
				nb.Endpoint = fresh.Endpoint
			}
			if fresh.Image != "" {
				nb.Image = fresh.Image
			}
			_ = m.repo.Update(bgCtx, nb)
			_ = m.vols.Update(bgCtx, &volCopy)
		}
	}()

	m.emit(projectID, "notebook.creating", map[string]any{"name": name, "volume_id": vol.ID})
	return nb, nil
}

// Stop halts the server process but keeps the DB record and volume.
// Status transitions: running -> stopping -> stopped via worker observation.
func (m *Manager) Stop(ctx context.Context, projectID, name string) error {
	if projectID == "" {
		return fmt.Errorf("notebook: project ID is required")
	}
	nb, err := m.repo.Get(ctx, projectID, name)
	if err != nil || nb == nil {
		return fmt.Errorf("notebook %q not found", name)
	}
	if nb.Status == StatusStopped || nb.Status == StatusStopping {
		return nil
	}
	if err := m.repo.SetStatus(ctx, projectID, name, StatusStopping); err != nil {
		return fmt.Errorf("notebook: set status stopping: %w", err)
	}
	if err := m.driver.Stop(ctx, nb); err != nil {
		// A transport failure says nothing about workload state. Restore the last
		// observed status and let the caller retry when the worker is reachable.
		if restoreErr := m.repo.SetStatus(ctx, projectID, name, nb.Status); restoreErr != nil {
			return fmt.Errorf("notebook: stop failed: %v; restore status: %w", err, restoreErr)
		}
		return fmt.Errorf("notebook: stop: %w", err)
	}
	// Stop RPC delivered: worker will push StatusStopped when the process exits.
	return nil
}

// Restart relaunches a stopped notebook on the same worker that holds its volume.
// Returns immediately with status=starting; worker callback sets status to running/failed.
func (m *Manager) Restart(ctx context.Context, projectID, name string) error {
	if projectID == "" {
		return fmt.Errorf("notebook: project ID is required")
	}
	nb, err := m.repo.Get(ctx, projectID, name)
	if err != nil || nb == nil {
		return fmt.Errorf("notebook %q not found", name)
	}
	if nb.Status == StatusRunning || nb.Status == StatusStarting || nb.Status == StatusProvisioning {
		return nil // already starting/running
	}

	var vol *NotebookVolume
	if nb.VolumeID != "" {
		vol, _ = m.vols.Get(ctx, nb.VolumeID)
	}
	if vol == nil {
		if nb.WorkDir == "" {
			return fmt.Errorf("notebook %q: no volume and no work_dir; cannot restart", name)
		}
		// Construct a minimal volume for notebooks created before the volume system.
		vol = &NotebookVolume{ProjectID: projectID, ID: nb.VolumeID, WorkDir: nb.WorkDir}
	}

	spec := Notebook{}
	spec.Metadata.Name = nb.Name
	if nb.YAML != "" {
		_ = parseYAML(nb.YAML, &spec)
	}
	if err := spec.Spec.Prepare.Validate(); err != nil {
		return fmt.Errorf("notebook: invalid prepare spec: %w", err)
	}
	spec.Metadata.ProjectID = projectID

	if err := m.repo.SetStatus(ctx, projectID, name, StatusStarting); err != nil {
		return fmt.Errorf("notebook: set status starting: %w", err)
	}

	bgCtx := context.WithoutCancel(ctx)
	volCopy := *vol
	restartYAML := nb.YAML
	go func() {
		fresh, err := m.driver.Start(bgCtx, spec, &volCopy, restartYAML)
		if err != nil {
			slog.Error("notebook: restart failed", "name", name, "err", err)
			_ = m.repo.SetStatus(bgCtx, projectID, name, StatusFailed)
			return
		}
		if nb, err := m.repo.Get(bgCtx, projectID, name); err == nil && nb != nil {
			volCopy.Status = VolumeStatusBound
			if fresh.WorkerID != "" {
				nb.WorkerID = fresh.WorkerID
				volCopy.WorkerID = fresh.WorkerID
			}
			if fresh.Token != "" {
				nb.Token = fresh.Token
			}
			if fresh.Endpoint != "" {
				nb.Endpoint = fresh.Endpoint
			}
			if fresh.WorkDir != "" {
				nb.WorkDir = fresh.WorkDir
			}
			if fresh.PID != 0 {
				nb.PID = fresh.PID
			}
			_ = m.repo.Update(bgCtx, nb)
			_ = m.vols.Update(bgCtx, &volCopy)
		}
	}()
	return nil
}

// Delete stops the server (if running), removes the server record, and
// sets the backing volume status to "released". The work directory is preserved
// so the volume can be attached to a new server later.
func (m *Manager) Delete(ctx context.Context, projectID, name string) error {
	if projectID == "" {
		return fmt.Errorf("notebook: project ID is required")
	}
	nb, err := m.repo.Get(ctx, projectID, name)
	if err != nil || nb == nil {
		return fmt.Errorf("notebook %q not found", name)
	}

	if nb.Status == StatusRunning {
		if err := m.driver.Stop(ctx, nb); err != nil {
			slog.Warn("notebook driver stop failed during delete", "name", name, "err", err)
		}
	}

	// Remove the server record.
	if err := m.repo.Delete(ctx, projectID, name); err != nil {
		return fmt.Errorf("notebook: delete record: %w", err)
	}

	// Release the volume so it can be reattached.
	if nb.VolumeID != "" {
		if err := m.vols.SetStatus(ctx, nb.VolumeID, VolumeStatusReleased); err != nil {
			slog.Warn("notebook: release volume failed", "vol_id", nb.VolumeID, "err", err)
		}
	}

	m.emit(projectID, "notebook.deleted", map[string]any{"name": name})
	return nil
}

// PurgeVolume permanently deletes a released volume's backing storage and removes
// the volume record. The volume must be in "released" status (not bound to a server).
func (m *Manager) PurgeVolume(ctx context.Context, projectID, volumeID string) error {
	if projectID == "" {
		return fmt.Errorf("notebook: project ID is required")
	}
	vol, err := m.vols.Get(ctx, volumeID)
	if err != nil || vol == nil {
		return fmt.Errorf("notebook: volume %q not found", volumeID)
	}
	if vol.ProjectID != projectID {
		return fmt.Errorf("notebook: volume %q not found", volumeID)
	}
	if vol.Status != VolumeStatusReleased {
		return fmt.Errorf("notebook: volume %q is not released (status: %s); delete the server first", volumeID, vol.Status)
	}

	if err := m.driver.DeprovisionVolume(ctx, vol); err != nil {
		slog.Warn("notebook: deprovision volume failed during purge", "vol_id", volumeID, "err", err)
	}

	if err := m.vols.Delete(ctx, volumeID); err != nil {
		return fmt.Errorf("notebook: delete volume record: %w", err)
	}
	m.emit(vol.ProjectID, "notebook.volume.purged", map[string]any{"volume_id": volumeID})
	return nil
}

// UpdateStatus applies a project-scoped status update from a worker agent.
func (m *Manager) UpdateStatus(ctx context.Context, projectID, agentID, name, status, endpoint, workDir, token string, pid int, env string) error {
	if projectID == "" {
		return fmt.Errorf("notebook: project ID is required")
	}
	nb, err := m.repo.Get(ctx, projectID, name)
	if err != nil || nb == nil {
		return fmt.Errorf("notebook %q not found", name)
	}
	if agentID != "" && nb.WorkerID != "" && nb.WorkerID != agentID {
		return fmt.Errorf("notebook %q owned by worker %q, push from %q rejected", name, nb.WorkerID, agentID)
	}
	previousStatus := nb.Status
	if status != "" {
		nb.Status = status
	}
	if endpoint != "" {
		nb.Endpoint = endpoint
	}
	if workDir != "" {
		nb.WorkDir = workDir
	}
	if token != "" {
		nb.Token = token
	}
	if pid != 0 {
		nb.PID = pid
	}
	if env != "" {
		nb.Env = env
	}
	if status == StatusStopped || status == StatusFailed {
		nb.Endpoint = ""
		nb.PID = 0
		nb.Token = ""
	}

	if workDir != "" && nb.VolumeID != "" {
		if vol, err := m.vols.Get(ctx, nb.VolumeID); err == nil && vol != nil && vol.WorkDir != workDir {
			vol.WorkDir = workDir
			_ = m.vols.Update(ctx, vol)
		}
	}

	if err := m.repo.Update(ctx, nb); err != nil {
		return fmt.Errorf("notebook: update: %w", err)
	}
	if status == "" || status == previousStatus {
		return nil
	}
	switch status {
	case StatusRunning:
		m.emit(projectID, "notebook.running", map[string]any{"name": name})
	case StatusStopped:
		m.emit(projectID, "notebook.stopped", map[string]any{"name": name})
	case StatusFailed:
		m.emit(projectID, "notebook.failed", map[string]any{"name": name})
	default:
		m.emit(projectID, "notebook.status", map[string]any{"name": name, "status": status})
	}
	return nil
}

// SyncAgent requests current notebook status from a specific agent.
// Called when an agent (re)connects so master DB reflects any state changes
// that occurred while the agent was offline or gRPC was disconnected.
func (m *Manager) SyncAgent(ctx context.Context, agentID string) {
	syncer, ok := m.driver.(StatusSyncer)
	if !ok {
		return
	}
	servers, err := m.repo.ListByWorker(ctx, agentID)
	if err != nil {
		return
	}
	active := make([]*NotebookServer, 0)
	for _, s := range servers {
		if s.WorkerID == agentID && (s.Status == StatusStarting || s.Status == StatusRunning || s.Status == StatusStopping) {
			active = append(active, s)
		}
	}
	if len(active) == 0 {
		return
	}
	apply := func(projectID, name, status string) {
		if err := m.UpdateStatus(ctx, projectID, agentID, name, status, "", "", "", 0, ""); err != nil {
			slog.Warn("notebook sync apply failed", "agent", agentID, "project", projectID, "name", name, "status", status, "err", err)
		}
	}
	_ = syncer.SyncStatus(ctx, active, apply)
}

func (m *Manager) emit(projectID, eventType string, fields map[string]any) {
	if m.events != nil {
		m.events.Publish(event.New(projectID, eventType, fields))
	}
}

func parseYAML(src string, dst any) error {
	return yaml.Unmarshal([]byte(src), dst)
}
