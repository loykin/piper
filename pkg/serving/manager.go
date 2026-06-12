package serving

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/piper/piper/internal/artifact"
	"github.com/piper/piper/internal/event"
)

// Manager handles the lifecycle of ModelService deployments.
// It delegates actual process/pod management to a Driver.
type Manager struct {
	repo   Repository
	driver Driver
	events event.Publisher // nil means no event publishing
}

// SetEventPublisher wires an event publisher so the manager can emit service lifecycle events.
func (m *Manager) SetEventPublisher(p event.Publisher) {
	m.events = p
}

// New creates a Manager with the given driver.
// driver must not be nil.
func New(repo Repository, driver Driver) *Manager {
	return &Manager{repo: repo, driver: driver}
}

// ArtifactTarget returns the artifact delivery mode expected by the underlying driver.
func (m *Manager) ArtifactTarget() artifact.Target { return m.driver.ArtifactTarget() }

// Deploy starts a ModelService. Artifact resolution must happen before calling Deploy.
func (m *Manager) Deploy(ctx context.Context, projectID string, svc ModelService, art artifact.Resolved, yamlStr string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	name := svc.Metadata.Name
	svc.Metadata.ProjectID = projectID

	artifactLabel := ""
	if svc.Spec.Model.FromArtifact != nil {
		artifactLabel = svc.Spec.Model.FromArtifact.Step + "/" + svc.Spec.Model.FromArtifact.Artifact
	} else if svc.Spec.Model.FromURI != "" {
		artifactLabel = svc.Spec.Model.FromURI
	}

	rec, err := m.driver.Deploy(ctx, svc, art, yamlStr)
	if err != nil {
		return err
	}

	// Merge driver-returned record with known metadata.
	if rec.Artifact == "" {
		rec.Artifact = artifactLabel
	}
	if rec.Name == "" {
		rec.Name = name
	}
	rec.RunID = art.RunID
	rec.ProjectID = projectID
	rec.YAML = yamlStr

	if err := m.repo.Upsert(ctx, rec); err != nil {
		return err
	}
	m.emit(projectID, "service.deployed", map[string]any{"name": name, "artifact": artifactLabel})
	return nil
}

// Stop terminates a running service.
func (m *Manager) Stop(ctx context.Context, projectID, name string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	svc, err := m.repo.Get(ctx, projectID, name)
	if err != nil {
		return fmt.Errorf("get service: %w", err)
	}
	if svc == nil {
		return fmt.Errorf("service %q not found", name)
	}

	if svc.Status == StatusStopped || svc.Status == StatusStopping {
		return nil
	}
	if err := m.repo.SetStatus(ctx, projectID, name, StatusStopping); err != nil {
		return fmt.Errorf("set service stopping: %w", err)
	}
	if err := m.driver.Stop(ctx, svc); err != nil {
		if restoreErr := m.repo.SetStatus(ctx, projectID, name, svc.Status); restoreErr != nil {
			return fmt.Errorf("stop service: %v; restore status: %w", err, restoreErr)
		}
		return fmt.Errorf("stop service: %w", err)
	}
	return nil
}

// Restart stops and re-deploys a service with the resolved artifact.
func (m *Manager) Restart(ctx context.Context, projectID string, svc ModelService, art artifact.Resolved, yamlStr string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	_ = m.Stop(ctx, projectID, svc.Metadata.Name)
	return m.Deploy(ctx, projectID, svc, art, yamlStr)
}

// SetYAML stores the original YAML on the service record.
func (m *Manager) SetYAML(ctx context.Context, projectID, name, yaml string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	svc, err := m.repo.Get(ctx, projectID, name)
	if err != nil || svc == nil {
		return fmt.Errorf("service %q not found", name)
	}
	svc.YAML = yaml
	return m.repo.Update(ctx, svc)
}

// UpdateStatus applies backend-observed state from the owning worker.
func (m *Manager) UpdateStatus(ctx context.Context, projectID, agentID, name, status, endpoint string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	svc, err := m.repo.Get(ctx, projectID, name)
	if err != nil || svc == nil {
		return fmt.Errorf("service %q not found", name)
	}
	if agentID != "" && svc.WorkerID != "" && svc.WorkerID != agentID {
		return fmt.Errorf("service %q owned by worker %q, push from %q rejected", name, svc.WorkerID, agentID)
	}
	previousStatus := svc.Status
	if status == "" {
		status = previousStatus
	}
	if err := m.repo.SetStatusEndpoint(ctx, projectID, name, status, endpoint); err != nil {
		return err
	}
	if status == previousStatus {
		return nil
	}
	m.emit(projectID, "service.status", map[string]any{"name": name, "status": status})
	return nil
}

func (m *Manager) SyncAgent(ctx context.Context, agentID string) {
	syncer, ok := m.driver.(StatusSyncer)
	if !ok {
		return
	}
	services, err := m.repo.ListByWorker(ctx, agentID)
	if err != nil {
		return
	}
	active := make([]*Service, 0)
	for _, svc := range services {
		if svc.WorkerID == agentID && svc.Status != StatusStopped {
			active = append(active, svc)
		}
	}
	if len(active) == 0 {
		return
	}
	_ = syncer.SyncStatus(ctx, active, func(projectID, name, status string) {
		if err := m.UpdateStatus(ctx, projectID, agentID, name, status, ""); err != nil {
			slog.Warn("serving sync apply failed", "agent", agentID, "project", projectID, "name", name, "status", status, "err", err)
		}
	})
}

func (m *Manager) emit(projectID, eventType string, fields map[string]any) {
	if m.events != nil {
		m.events.Publish(event.New(projectID, eventType, fields))
	}
}
