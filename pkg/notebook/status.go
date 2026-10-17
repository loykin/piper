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

// StatusReport is what a runtime driver observed about a notebook. Empty
// fields leave the record unchanged, except Reason: it always travels with a
// non-empty Status, so a recovered notebook doesn't keep an old failure.
type StatusReport struct {
	Status   string
	Reason   string
	Endpoint string
	WorkDir  string
	Token    string
	PID      int
	Env      string
}

func (s *StatusSink) Update(ctx context.Context, projectID, runtimeID, name string, r StatusReport) error {
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
	if r.Status != "" {
		nb.Status = r.Status
		nb.Reason = r.Reason
	}
	if r.Endpoint != "" {
		nb.Endpoint = r.Endpoint
	}
	if r.WorkDir != "" {
		nb.WorkDir = r.WorkDir
	}
	if r.Token != "" {
		nb.Token = r.Token
	}
	if r.PID != 0 {
		nb.PID = r.PID
	}
	if r.Env != "" {
		nb.Env = r.Env
	}
	if r.Status == StatusStopped || r.Status == StatusFailed {
		nb.Endpoint = ""
		nb.PID = 0
		nb.Token = ""
	}
	if r.WorkDir != "" && nb.VolumeID != "" {
		if vol, err := s.vols.Get(ctx, nb.VolumeID); err == nil && vol != nil && vol.WorkDir != r.WorkDir {
			vol.WorkDir = r.WorkDir
			_ = s.vols.Update(ctx, vol)
		}
	}
	if err := s.repo.Update(ctx, nb); err != nil {
		return fmt.Errorf("notebook: update: %w", err)
	}
	if r.Status == "" || r.Status == previousStatus {
		return nil
	}
	eventType := "notebook.status"
	fields := map[string]any{"name": name, "status": r.Status}
	switch r.Status {
	case StatusRunning:
		eventType = "notebook.running"
		fields = map[string]any{"name": name}
	case StatusStopped:
		eventType = "notebook.stopped"
		fields = map[string]any{"name": name}
	case StatusFailed:
		eventType = "notebook.failed"
		fields = map[string]any{"name": name, "error": r.Reason}
	}
	s.mu.RLock()
	publisher := s.events
	s.mu.RUnlock()
	if publisher != nil {
		publisher.Publish(event.New(projectID, eventType, fields))
	}
	return nil
}
