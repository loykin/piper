package serving

import (
	"fmt"
	"sync"
	"time"

	"github.com/piper/piper/internal/agent"
)

const workerTTL = 60 * time.Second

// ServingWorkerInfo holds registration metadata for a serving worker agent.
type ServingWorkerInfo struct {
	ID       string    `json:"id"`
	Addr     string    `json:"addr"` // e.g. http://10.0.0.5:7700
	GPUs     []string  `json:"gpus"`
	Hostname string    `json:"hostname"`
	LastSeen time.Time `json:"last_seen"`
}

// ServingWorkerRegistry tracks registered serving worker agents.
type ServingWorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]*ServingWorkerInfo
	agents  *agent.Registry
}

// NewServingWorkerRegistry creates a registry and starts the TTL reaper.
func NewServingWorkerRegistry() *ServingWorkerRegistry {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}
	go r.reap()
	return r
}

func (r *ServingWorkerRegistry) SetAgentRegistry(agents *agent.Registry) {
	r.mu.Lock()
	r.agents = agents
	r.mu.Unlock()
}

// Register adds or updates a worker entry.
func (r *ServingWorkerRegistry) Register(info *ServingWorkerInfo) {
	info.LastSeen = time.Now()
	r.mu.Lock()
	r.workers[info.ID] = info
	agents := r.agents
	r.mu.Unlock()
	if agents != nil {
		agents.Register(agent.FromServingWorker(agent.ServingWorker{
			ID:       info.ID,
			Addr:     info.Addr,
			GPUs:     info.GPUs,
			Hostname: info.Hostname,
		}))
	}
}

// Heartbeat refreshes the LastSeen timestamp for a worker.
func (r *ServingWorkerRegistry) Heartbeat(id string) {
	r.mu.Lock()
	if w, ok := r.workers[id]; ok {
		w.LastSeen = time.Now()
	}
	agents := r.agents
	r.mu.Unlock()
	if agents != nil {
		_ = agents.Heartbeat(id)
	}
}

// Pick returns the first available worker. Returns an error if none are registered.
func (r *ServingWorkerRegistry) Pick() (*ServingWorkerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.workers {
		if time.Since(w.LastSeen) < workerTTL {
			return w, nil
		}
	}
	return nil, fmt.Errorf("no serving worker available")
}

// Get returns a specific worker by ID, or an error if not found or expired.
func (r *ServingWorkerRegistry) Get(id string) (*ServingWorkerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[id]
	if !ok || time.Since(w.LastSeen) >= workerTTL {
		return nil, fmt.Errorf("serving worker %q not available", id)
	}
	return w, nil
}

// GetByHostname returns the first live worker matching the given hostname.
func (r *ServingWorkerRegistry) GetByHostname(hostname string) (*ServingWorkerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.workers {
		if w.Hostname == hostname && time.Since(w.LastSeen) < workerTTL {
			return w, nil
		}
	}
	return nil, fmt.Errorf("serving worker with hostname %q not available", hostname)
}

// List returns all registered workers.
func (r *ServingWorkerRegistry) List() []*ServingWorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ServingWorkerInfo, 0, len(r.workers))
	for _, w := range r.workers {
		cp := *w
		out = append(out, &cp)
	}
	return out
}

// Remove deregisters a worker by ID.
func (r *ServingWorkerRegistry) Remove(id string) {
	r.mu.Lock()
	delete(r.workers, id)
	r.mu.Unlock()
}

// reap periodically removes workers that have not sent a heartbeat within TTL.
func (r *ServingWorkerRegistry) reap() {
	ticker := time.NewTicker(workerTTL / 2)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		for id, w := range r.workers {
			if time.Since(w.LastSeen) >= workerTTL {
				delete(r.workers, id)
			}
		}
		agents := r.agents
		r.mu.Unlock()
		if agents != nil {
			agents.Cleanup()
		}
	}
}
