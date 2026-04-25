package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const workerTTL = 30 * time.Second

// WorkerTTL is the exported TTL constant for use by other packages.
const WorkerTTL = workerTTL

// Registry is the in-memory worker registry.
type Registry struct {
	mu      sync.Mutex
	workers map[string]*Info
	repo    Repository // nil means DB is not used (embedded mode)
}

// NewRegistry creates a new Registry backed by the given repository.
func NewRegistry(repo Repository) *Registry {
	return &Registry{
		workers: make(map[string]*Info),
		repo:    repo,
	}
}

// Register registers a Worker. Re-registering with the same ID updates it.
func (r *Registry) Register(info Info) {
	now := time.Now()
	info.RegisteredAt = now
	info.LastSeen = now
	info.Status = StatusOnline

	r.mu.Lock()
	r.workers[info.ID] = &info
	r.mu.Unlock()

	if r.repo != nil {
		if err := r.repo.Upsert(context.Background(), &WorkerRecord{
			ID:           info.ID,
			Label:        info.Label,
			Hostname:     info.Hostname,
			Concurrency:  info.Concurrency,
			Status:       StatusOnline,
			InFlight:     0,
			RegisteredAt: now,
			LastSeenAt:   now,
		}); err != nil {
			slog.Warn("registry: upsert worker failed", "id", info.ID, "err", err)
		}
	}
}

// Heartbeat updates the Worker's LastSeen and InFlight values.
func (r *Registry) Heartbeat(id string, inFlight int) error {
	r.mu.Lock()
	w, ok := r.workers[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("worker %s not registered", id)
	}
	w.LastSeen = time.Now()
	w.InFlight = inFlight
	r.mu.Unlock()

	if r.repo != nil {
		if err := r.repo.Heartbeat(context.Background(), id, inFlight); err != nil {
			slog.Warn("registry: heartbeat db failed", "id", id, "err", err)
		}
	}
	return nil
}

// Touch updates LastSeen on each poll request. Ignores unregistered workers.
func (r *Registry) Touch(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	if w, ok := r.workers[id]; ok {
		w.LastSeen = time.Now()
	}
	r.mu.Unlock()
}

// List returns the list of active workers that have responded within the TTL.
func (r *Registry) List() []Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-workerTTL)
	out := make([]Info, 0, len(r.workers))
	for _, w := range r.workers {
		if w.LastSeen.After(cutoff) {
			out = append(out, *w)
		}
	}
	return out
}

// Cleanup removes expired workers from memory and marks them as offline in the DB.
func (r *Registry) Cleanup() {
	cutoff := time.Now().Add(-workerTTL)

	r.mu.Lock()
	for id, w := range r.workers {
		if !w.LastSeen.After(cutoff) {
			delete(r.workers, id)
		}
	}
	r.mu.Unlock()

	if r.repo != nil {
		if err := r.repo.MarkOffline(context.Background(), cutoff); err != nil {
			slog.Warn("registry: mark offline failed", "err", err)
		}
	}
}
