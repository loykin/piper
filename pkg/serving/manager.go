package serving

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/event"
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
func (m *Manager) Deploy(ctx context.Context, svc ModelService, art artifact.Resolved, yamlStr string) error {
	name := svc.Metadata.Name

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
	rec.YAML = yamlStr

	if err := m.repo.Upsert(ctx, rec); err != nil {
		return err
	}
	m.emit("service.deployed", map[string]any{"name": name, "artifact": artifactLabel})
	return nil
}

// Stop terminates a running service.
func (m *Manager) Stop(ctx context.Context, name string) error {
	svc, err := m.repo.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("get service: %w", err)
	}
	if svc == nil {
		return fmt.Errorf("service %q not found", name)
	}

	if svc.Status == StatusStopped || svc.Status == StatusStopping {
		return nil
	}
	if err := m.repo.SetStatus(ctx, name, StatusStopping); err != nil {
		return fmt.Errorf("set service stopping: %w", err)
	}
	if err := m.driver.Stop(ctx, svc); err != nil {
		if restoreErr := m.repo.SetStatus(ctx, name, svc.Status); restoreErr != nil {
			return fmt.Errorf("stop service: %v; restore status: %w", err, restoreErr)
		}
		return fmt.Errorf("stop service: %w", err)
	}
	return nil
}

// Restart stops and re-deploys a service with the resolved artifact.
func (m *Manager) Restart(ctx context.Context, svc ModelService, art artifact.Resolved, yamlStr string) error {
	_ = m.Stop(ctx, svc.Metadata.Name)
	return m.Deploy(ctx, svc, art, yamlStr)
}

// SetYAML stores the original YAML on the service record.
func (m *Manager) SetYAML(ctx context.Context, name, yaml string) error {
	svc, err := m.repo.Get(ctx, name)
	if err != nil || svc == nil {
		return fmt.Errorf("service %q not found", name)
	}
	svc.YAML = yaml
	return m.repo.Update(ctx, svc)
}

// UpdateStatus applies backend-observed state from the owning worker.
func (m *Manager) UpdateStatus(ctx context.Context, agentID, name, status, endpoint string) error {
	svc, err := m.repo.Get(ctx, name)
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
	if err := m.repo.SetStatusEndpoint(ctx, name, status, endpoint); err != nil {
		return err
	}
	if status == previousStatus {
		return nil
	}
	m.emit("service.status", map[string]any{"name": name, "status": status})
	return nil
}

func (m *Manager) SyncAgent(ctx context.Context, agentID string) {
	syncer, ok := m.driver.(StatusSyncer)
	if !ok {
		return
	}
	services, err := m.repo.List(ctx)
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
	_ = syncer.SyncStatus(ctx, active, func(name, status string) {
		if err := m.UpdateStatus(ctx, agentID, name, status, ""); err != nil {
			slog.Warn("serving sync apply failed", "agent", agentID, "name", name, "status", status, "err", err)
		}
	})
}

func (m *Manager) emit(eventType string, fields map[string]any) {
	if m.events != nil {
		m.events.Publish(event.New(eventType, fields))
	}
}
