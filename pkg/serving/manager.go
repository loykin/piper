package serving

import (
	"context"
	"fmt"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/pkg/security"
)

// Manager handles the lifecycle of ModelService deployments.
// It delegates actual process/pod management to a Driver.
type Manager struct {
	repo    Repository
	driver  Driver
	status  *StatusSink
	runtime string
	events  event.Publisher // nil means no event publishing
}

// SetRuntime sets the one runtime accepted at lifecycle boundaries.
func (m *Manager) SetRuntime(runtime string) { m.runtime = runtime }

// SetEventPublisher wires an event publisher so the manager can emit service lifecycle events.
func (m *Manager) SetEventPublisher(p event.Publisher) {
	m.events = p
	m.status.SetEventPublisher(p)
}

// New creates a Manager with the given driver.
// driver must not be nil.
func New(repo Repository, driver Driver) *Manager {
	return NewWithStatusSink(repo, driver, NewStatusSink(repo))
}

// NewWithStatusSink creates a Manager sharing the sink used by runtime
// drivers for observed status updates.
func NewWithStatusSink(repo Repository, driver Driver, status *StatusSink) *Manager {
	if status == nil {
		status = NewStatusSink(repo)
	}
	return &Manager{repo: repo, driver: driver, status: status}
}

// ArtifactTarget returns the artifact delivery mode expected by the underlying driver.
func (m *Manager) ArtifactTarget() artifact.Target { return m.driver.ArtifactTarget() }

// Deploy starts a ModelService. Artifact resolution must happen before calling Deploy.
func (m *Manager) Deploy(ctx context.Context, projectID string, svc ModelService, art artifact.Resolved, yamlStr string) error {
	if err := svc.Validate(); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	if err := ValidateDirectPlacement(svc, m.runtime); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
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
	if identity, ok := security.IdentityFromContext(ctx); ok {
		rec.CreatedBy = identity.ID
	}

	// A redeploy overwrites the current row (Upsert), so the version it
	// replaces must be preserved in history first — otherwise "v1 -> v2 ->
	// v3" leaves no trace that v1/v2 ever ran, only the final delete does.
	if existing, err := m.repo.Get(ctx, projectID, name); err == nil && existing != nil {
		if err := m.repo.AppendHistory(ctx, existing); err != nil {
			return fmt.Errorf("archive previous deployment: %w", err)
		}
	}

	if err := m.repo.Upsert(ctx, rec); err != nil {
		return err
	}
	m.emit(projectID, "service.deployed", map[string]any{"name": name, "artifact": artifactLabel})
	return nil
}

// Replace stops an existing service, deploys the replacement, and returns the
// single record persisted by Deploy. It is the application-level redeploy
// boundary; callers must resolve the artifact before entering it.
func (m *Manager) Replace(ctx context.Context, projectID string, svc ModelService, art artifact.Resolved, yamlStr string) (*Service, error) {
	existing, err := m.repo.Get(ctx, projectID, svc.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("get existing service: %w", err)
	}
	if existing != nil {
		if err := m.Stop(ctx, projectID, svc.Metadata.Name); err != nil {
			return nil, err
		}
	}
	if err := m.Deploy(ctx, projectID, svc, art, yamlStr); err != nil {
		return nil, err
	}
	rec, err := m.repo.Get(ctx, projectID, svc.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("get deployed service: %w", err)
	}
	if rec == nil {
		return nil, fmt.Errorf("deployed service %q was not persisted", svc.Metadata.Name)
	}
	return rec, nil
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
	// Record the real terminal outcome. Left at StatusStopping, a caller
	// that immediately deletes the service right after Stop (the UI's
	// delete flow) would archive "stopping" as the history's Final
	// Status forever — never actually terminal.
	if err := m.repo.SetStatus(ctx, projectID, name, StatusStopped); err != nil {
		return fmt.Errorf("stop service: mark stopped: %w", err)
	}
	return nil
}

// Restart stops and re-deploys a service with the resolved artifact.
func (m *Manager) Restart(ctx context.Context, projectID string, svc ModelService, art artifact.Resolved, yamlStr string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	_, err := m.Replace(ctx, projectID, svc, art, yamlStr)
	return err
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

// UpdateStatus applies backend-observed state through the shared runtime sink.
func (m *Manager) UpdateStatus(ctx context.Context, projectID, runtimeID, name, status, endpoint string) error {
	return m.status.Update(ctx, projectID, runtimeID, name, status, endpoint)
}

func (m *Manager) emit(projectID, eventType string, fields map[string]any) {
	if m.events != nil {
		m.events.Publish(event.New(projectID, eventType, fields))
	}
}
