package notebook

import (
	"context"
	"fmt"
	"sync"

	"github.com/loykin/piper/internal/event"
)

// StatusSink persists runtime-observed notebook state independently from the
// lifecycle Manager, avoiding a runtime-driver-to-manager callback cycle.
type StatusSink struct {
	repo   Repository
	vols   VolumeRepository
	mu     sync.RWMutex
	events event.Publisher
}

func NewStatusSink(repo Repository, vols VolumeRepository) *StatusSink {
	return &StatusSink{repo: repo, vols: vols}
}

func (s *StatusSink) SetEventPublisher(p event.Publisher) {
	s.mu.Lock()
	s.events = p
	s.mu.Unlock()
}

func (s *StatusSink) Update(ctx context.Context, projectID, runtimeID, name, status, endpoint, workDir, token string, pid int, env string) error {
	if projectID == "" {
		return fmt.Errorf("notebook: project ID is required")
	}
	nb, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return fmt.Errorf("notebook: get status target: %w", err)
	}
	if nb == nil {
		return fmt.Errorf("%w: notebook %q", ErrNotFound, name)
	}
	if runtimeID != "" && nb.RuntimeID != "" && nb.RuntimeID != runtimeID {
		return fmt.Errorf("notebook %q owned by runtime %q, update from %q rejected", name, nb.RuntimeID, runtimeID)
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
		if vol, err := s.vols.Get(ctx, nb.VolumeID); err == nil && vol != nil && vol.WorkDir != workDir {
			vol.WorkDir = workDir
			_ = s.vols.Update(ctx, vol)
		}
	}
	if err := s.repo.Update(ctx, nb); err != nil {
		return fmt.Errorf("notebook: update: %w", err)
	}
	if status == "" || status == previousStatus {
		return nil
	}
	eventType := "notebook.status"
	fields := map[string]any{"name": name, "status": status}
	switch status {
	case StatusRunning:
		eventType = "notebook.running"
		fields = map[string]any{"name": name}
	case StatusStopped:
		eventType = "notebook.stopped"
		fields = map[string]any{"name": name}
	case StatusFailed:
		eventType = "notebook.failed"
		fields = map[string]any{"name": name}
	}
	s.mu.RLock()
	publisher := s.events
	s.mu.RUnlock()
	if publisher != nil {
		publisher.Publish(event.New(projectID, eventType, fields))
	}
	return nil
}
