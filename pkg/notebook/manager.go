package notebook

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/piper/piper/pkg/event"
)

// Manager handles the lifecycle of NotebookServer instances.
// It delegates actual process management to a Driver.
type Manager struct {
	repo   Repository
	driver Driver
	events event.Publisher
}

// New creates a Manager backed by the given repository and driver.
// driver must not be nil.
func New(repo Repository, driver Driver) *Manager {
	return &Manager{repo: repo, driver: driver}
}

// SetEventPublisher wires an event publisher for notebook lifecycle events.
func (m *Manager) SetEventPublisher(p event.Publisher) {
	m.events = p
}

// Start launches a notebook server according to the given spec and persists
// the record to the repository. yamlStr is the raw YAML stored for auditing.
func (m *Manager) Start(ctx context.Context, spec NotebookServerSpec, yamlStr string) (*NotebookServer, error) {
	name := spec.Metadata.Name
	if name == "" {
		return nil, fmt.Errorf("notebook: metadata.name is required")
	}

	nb, err := m.driver.Start(ctx, spec, yamlStr)
	if err != nil {
		return nil, fmt.Errorf("notebook: start: %w", err)
	}

	if nb.Name == "" {
		nb.Name = name
	}
	if nb.WorkDir == "" && spec.Spec.Runtime.WorkDir != "" {
		nb.WorkDir = spec.Spec.Runtime.WorkDir
	}

	if err := m.repo.Create(ctx, nb); err != nil {
		return nil, fmt.Errorf("notebook: persist record: %w", err)
	}

	m.emit("notebook.started", map[string]any{"name": name, "endpoint": nb.Endpoint})
	return nb, nil
}

// Stop kills the notebook process and marks it stopped.
func (m *Manager) Stop(ctx context.Context, name string) error {
	nb, err := m.repo.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("notebook: get %q: %w", name, err)
	}
	if nb == nil {
		return fmt.Errorf("notebook: %q not found", name)
	}

	if err := m.driver.Stop(ctx, nb); err != nil {
		slog.Warn("notebook driver stop failed", "name", name, "err", err)
	}

	if err := m.repo.SetStatus(ctx, name, StatusStopped); err != nil {
		return fmt.Errorf("notebook: set status: %w", err)
	}
	m.emit("notebook.stopped", map[string]any{"name": name})
	return nil
}

// UpdateStatus updates the status and endpoint of a notebook server.
// Used by notebook worker agents to report status changes back to master.
func (m *Manager) UpdateStatus(ctx context.Context, name, status, endpoint string) error {
	nb, err := m.repo.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("notebook: get %q: %w", name, err)
	}
	if nb == nil {
		return fmt.Errorf("notebook: %q not found", name)
	}
	if endpoint != "" {
		nb.Endpoint = endpoint
	}
	nb.Status = status
	if err := m.repo.Update(ctx, nb); err != nil {
		return fmt.Errorf("notebook: update: %w", err)
	}
	m.emit("notebook.status", map[string]any{"name": name, "status": status})
	return nil
}

func (m *Manager) emit(eventType string, fields map[string]any) {
	if m.events != nil {
		m.events.Publish(event.New(eventType, fields))
	}
}
