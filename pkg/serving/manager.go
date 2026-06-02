package serving

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"gopkg.in/yaml.v3"

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

	if err := m.driver.Stop(ctx, svc); err != nil {
		slog.Warn("driver stop failed", "name", name, "err", err)
	}

	if err := m.repo.SetStatus(ctx, name, StatusStopped); err != nil {
		return err
	}
	m.emit("service.stopped", map[string]any{"name": name})
	return nil
}

// Restart stops and re-deploys a service with the resolved artifact.
func (m *Manager) Restart(ctx context.Context, svc ModelService, art artifact.Resolved, yamlStr string) error {
	_ = m.Stop(ctx, svc.Metadata.Name)
	return m.Deploy(ctx, svc, art, yamlStr)
}

// CheckHealth polls the endpoint of each running service and updates its status.
func (m *Manager) CheckHealth(ctx context.Context) {
	services, err := m.repo.List(ctx)
	if err != nil {
		slog.Warn("list services for health check failed", "err", err)
		return
	}
	for _, svc := range services {
		if svc.Status == StatusStopped || svc.Endpoint == "" {
			continue
		}
		healthy := m.serviceHealthy(ctx, svc)
		next := StatusFailed
		if healthy {
			next = StatusRunning
		}
		if next != svc.Status {
			if err := m.repo.SetStatus(ctx, svc.Name, next); err != nil {
				slog.Warn("update service health status failed", "name", svc.Name, "status", next, "err", err)
			}
		}
	}
}

func (m *Manager) serviceHealthy(ctx context.Context, svc *Service) bool {
	var ms ModelService
	healthPath := "/"
	if svc.YAML != "" && yaml.Unmarshal([]byte(svc.YAML), &ms) == nil && ms.Spec.Runtime.HealthPath != "" {
		healthPath = ms.Spec.Runtime.HealthPath
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.Endpoint+healthPath, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode < 500
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

// UpdateStatus updates the status (and optionally endpoint) of a service by name.
// Used by worker callback endpoints.
func (m *Manager) UpdateStatus(ctx context.Context, name, status, endpoint string) error {
	svc, err := m.repo.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("get service: %w", err)
	}
	if svc == nil {
		return fmt.Errorf("service %q not found", name)
	}
	svc.Status = status
	if endpoint != "" {
		svc.Endpoint = endpoint
	}
	if err := m.repo.Update(ctx, svc); err != nil {
		return err
	}
	m.emit("service.status", map[string]any{"name": name, "status": status})
	return nil
}

func (m *Manager) emit(eventType string, fields map[string]any) {
	if m.events != nil {
		m.events.Publish(event.New(eventType, fields))
	}
}
