package notebook

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/piper/piper/internal/agent"
)

const workerTTL = 60 * time.Second

// NotebookWorkerInfo holds registration metadata for a notebook worker agent.
type NotebookWorkerInfo struct {
	ID       string    `json:"id"`
	Addr     string    `json:"addr"` // e.g. http://10.0.0.5:7701
	Mode     string    `json:"mode,omitempty"`
	GPUs     []string  `json:"gpus"`
	Hostname string    `json:"hostname"`
	LastSeen time.Time `json:"last_seen"`
}

// NotebookWorkerRegistry tracks registered notebook worker agents.
type NotebookWorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]*NotebookWorkerInfo
	agents  *agent.Registry
}

// NewNotebookWorkerRegistry creates a registry and starts the TTL reaper.
func NewNotebookWorkerRegistry() *NotebookWorkerRegistry {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}
	go r.reap()
	return r
}

func (r *NotebookWorkerRegistry) SetAgentRegistry(agents *agent.Registry) {
	r.mu.Lock()
	r.agents = agents
	r.mu.Unlock()
}

// Register adds or updates a worker entry.
func (r *NotebookWorkerRegistry) Register(info *NotebookWorkerInfo) {
	info.LastSeen = time.Now()
	r.mu.Lock()
	r.workers[info.ID] = info
	agents := r.agents
	r.mu.Unlock()
	if agents != nil {
		agents.Register(agent.FromNotebookWorker(agent.NotebookWorker{
			ID:       info.ID,
			Addr:     info.Addr,
			Mode:     info.Mode,
			GPUs:     info.GPUs,
			Hostname: info.Hostname,
		}))
	}
}

// Heartbeat refreshes the LastSeen timestamp for a worker.
func (r *NotebookWorkerRegistry) Heartbeat(id string) {
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
func (r *NotebookWorkerRegistry) Pick() (*NotebookWorkerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.workers {
		if time.Since(w.LastSeen) < workerTTL {
			return w, nil
		}
	}
	return nil, fmt.Errorf("no notebook worker available")
}

// PickForGPU returns an available worker that has all required GPU indices.
// gpus is a comma-separated list of GPU indices, e.g. "0,1".
func (r *NotebookWorkerRegistry) PickForGPU(gpus string) (*NotebookWorkerInfo, error) {
	required := strings.Split(gpus, ",")
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.workers {
		if time.Since(w.LastSeen) >= workerTTL {
			continue
		}
		if workerHasGPUs(w.GPUs, required) {
			return w, nil
		}
	}
	return nil, fmt.Errorf("no notebook worker with GPU(s) %q available", gpus)
}

func workerHasGPUs(available []string, required []string) bool {
	set := make(map[string]bool, len(available))
	for _, g := range available {
		set[strings.TrimSpace(g)] = true
	}
	for _, g := range required {
		if !set[strings.TrimSpace(g)] {
			return false
		}
	}
	return true
}

// Get returns a specific worker by ID, or an error if not found or expired.
func (r *NotebookWorkerRegistry) Get(id string) (*NotebookWorkerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[id]
	if !ok || time.Since(w.LastSeen) >= workerTTL {
		return nil, fmt.Errorf("notebook worker %q not available", id)
	}
	return w, nil
}

// GetByHostname returns the first live worker matching the given hostname.
func (r *NotebookWorkerRegistry) GetByHostname(hostname string) (*NotebookWorkerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.workers {
		if w.Hostname == hostname && time.Since(w.LastSeen) < workerTTL {
			return w, nil
		}
	}
	return nil, fmt.Errorf("notebook worker with hostname %q not available", hostname)
}

// List returns all registered workers.
func (r *NotebookWorkerRegistry) List() []*NotebookWorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*NotebookWorkerInfo, 0, len(r.workers))
	for _, w := range r.workers {
		cp := *w
		out = append(out, &cp)
	}
	return out
}

// Remove deregisters a worker by ID.
func (r *NotebookWorkerRegistry) Remove(id string) {
	r.mu.Lock()
	delete(r.workers, id)
	r.mu.Unlock()
}

// reap periodically removes workers that have not sent a heartbeat within TTL.
func (r *NotebookWorkerRegistry) reap() {
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
