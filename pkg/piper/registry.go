package piper

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/piper/piper/pkg/store"
)

const workerTTL = 30 * time.Second

// WorkerInfo holds worker information used by the API layer (in-memory cache)
type WorkerInfo struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Concurrency  int       `json:"concurrency"`
	Hostname     string    `json:"hostname"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
	Status       string    `json:"status"`
	InFlight     int       `json:"in_flight"`
}

type workerRegistry struct {
	mu      sync.Mutex
	workers map[string]*WorkerInfo
	store   *store.Store // nil means DB is not used (embedded mode)
}

func newWorkerRegistry(st *store.Store) *workerRegistry {
	return &workerRegistry{
		workers: make(map[string]*WorkerInfo),
		store:   st,
	}
}

// register registers a Worker. Re-registering with the same ID updates it.
func (r *workerRegistry) register(info WorkerInfo) {
	now := time.Now()
	info.RegisteredAt = now
	info.LastSeen = now
	info.Status = store.WorkerStatusOnline

	r.mu.Lock()
	r.workers[info.ID] = &info
	r.mu.Unlock()

	if r.store != nil {
		if err := r.store.UpsertWorker(&store.Worker{
			ID:           info.ID,
			Label:        info.Label,
			Hostname:     info.Hostname,
			Concurrency:  info.Concurrency,
			Status:       store.WorkerStatusOnline,
			InFlight:     0,
			RegisteredAt: now,
			LastSeenAt:   now,
		}); err != nil {
			slog.Warn("registry: upsert worker failed", "id", info.ID, "err", err)
		}
	}
}

// heartbeat updates the Worker's LastSeen and InFlight values.
func (r *workerRegistry) heartbeat(id string, inFlight int) error {
	r.mu.Lock()
	w, ok := r.workers[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("worker %s not registered", id)
	}
	w.LastSeen = time.Now()
	w.InFlight = inFlight
	r.mu.Unlock()

	if r.store != nil {
		if err := r.store.HeartbeatWorker(id, inFlight); err != nil {
			slog.Warn("registry: heartbeat db failed", "id", id, "err", err)
		}
	}
	return nil
}

// touch updates LastSeen on each poll request. Ignores unregistered workers.
func (r *workerRegistry) touch(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	if w, ok := r.workers[id]; ok {
		w.LastSeen = time.Now()
	}
	r.mu.Unlock()
}

// list returns the list of active workers that have responded within the TTL.
func (r *workerRegistry) list() []WorkerInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-workerTTL)
	out := make([]WorkerInfo, 0, len(r.workers))
	for _, w := range r.workers {
		if w.LastSeen.After(cutoff) {
			out = append(out, *w)
		}
	}
	return out
}

// cleanup removes expired workers from memory and marks them as offline in the DB.
func (r *workerRegistry) cleanup() {
	cutoff := time.Now().Add(-workerTTL)

	r.mu.Lock()
	for id, w := range r.workers {
		if !w.LastSeen.After(cutoff) {
			delete(r.workers, id)
		}
	}
	r.mu.Unlock()

	if r.store != nil {
		if err := r.store.MarkWorkersOffline(cutoff); err != nil {
			slog.Warn("registry: mark offline failed", "err", err)
		}
	}
}
