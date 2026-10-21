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
	repo      Repository
	mu        sync.RWMutex
	events    event.Publisher
	deploying map[[2]string]chan struct{}
}

func NewStatusSink(repo Repository) *StatusSink {
	return &StatusSink{repo: repo, deploying: make(map[[2]string]chan struct{})}
}

func (s *StatusSink) SetEventPublisher(p event.Publisher) {
	s.mu.Lock()
	s.events = p
	s.mu.Unlock()
}

// beginDeploy keeps observations behind the initial row write. A runtime can
// become observable before Driver.Deploy returns; treating that window as a
// missing service permanently poisons Kubernetes orphan tracking.
func (s *StatusSink) beginDeploy(ctx context.Context, projectID, name string) (func(), error) {
	key := [2]string{projectID, name}
	for {
		s.mu.Lock()
		pending := s.deploying[key]
		if pending == nil {
			pending = make(chan struct{})
			s.deploying[key] = pending
			s.mu.Unlock()
			return func() { s.mu.Lock(); delete(s.deploying, key); close(pending); s.mu.Unlock() }, nil
		}
		s.mu.Unlock()
		select {
		case <-pending:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *StatusSink) Update(ctx context.Context, projectID, runtimeID, name, status, endpoint string) error {
	if projectID == "" {
		return fmt.Errorf("serving: project ID is required")
	}
	for {
		s.mu.RLock()
		pending := s.deploying[[2]string{projectID, name}]
		s.mu.RUnlock()
		if pending == nil {
			break
		}
		select {
		case <-pending:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	svc, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return fmt.Errorf("serving: get status target: %w", err)
	}
	if svc == nil {
		return fmt.Errorf("%w: service %q", ErrNotFound, name)
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
