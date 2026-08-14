package serving

import (
	"context"
	"fmt"
	"sync"

	"github.com/loykin/piper/internal/event"
)

// StatusSink persists backend-observed service state independently from the
// lifecycle Manager. Runtime drivers report here, which avoids a
// driver-to-manager callback cycle in the composition root.
type StatusSink struct {
	repo   Repository
	mu     sync.RWMutex
	events event.Publisher
}

func NewStatusSink(repo Repository) *StatusSink { return &StatusSink{repo: repo} }

func (s *StatusSink) SetEventPublisher(p event.Publisher) {
	s.mu.Lock()
	s.events = p
	s.mu.Unlock()
}

func (s *StatusSink) Update(ctx context.Context, projectID, runtimeID, name, status, endpoint string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	svc, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return fmt.Errorf("serving: get status target: %w", err)
	}
	if svc == nil {
		return fmt.Errorf("service %q not found", name)
	}
	if runtimeID != "" && svc.RuntimeID != "" && svc.RuntimeID != runtimeID {
		return fmt.Errorf("service %q owned by runtime %q, update from %q rejected", name, svc.RuntimeID, runtimeID)
	}
	previousStatus := svc.Status
	if status == "" {
		status = previousStatus
	}
	if err := s.repo.SetStatusEndpoint(ctx, projectID, name, status, endpoint); err != nil {
		return err
	}
	if status == previousStatus {
		return nil
	}
	s.mu.RLock()
	publisher := s.events
	s.mu.RUnlock()
	if publisher != nil {
		publisher.Publish(event.New(projectID, "service.status", map[string]any{"name": name, "status": status}))
	}
	return nil
}
