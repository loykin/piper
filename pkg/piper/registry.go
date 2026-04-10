package piper

import (
	"fmt"
	"sync"
	"time"
)

const workerTTL = 30 * time.Second

// WorkerInfo는 등록된 worker의 상태
type WorkerInfo struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Concurrency  int       `json:"concurrency"`
	Hostname     string    `json:"hostname"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
}

type workerRegistry struct {
	mu      sync.Mutex
	workers map[string]*WorkerInfo
}

func newWorkerRegistry() *workerRegistry {
	return &workerRegistry{workers: make(map[string]*WorkerInfo)}
}

// register는 worker를 등록한다. 같은 ID로 재등록하면 갱신한다.
func (r *workerRegistry) register(info WorkerInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	info.RegisteredAt = now
	info.LastSeen = now
	r.workers[info.ID] = &info
}

// heartbeat는 worker의 LastSeen을 갱신한다.
func (r *workerRegistry) heartbeat(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		return fmt.Errorf("worker %s not registered", id)
	}
	w.LastSeen = time.Now()
	return nil
}

// touch는 폴링 요청마다 LastSeen을 갱신한다. 미등록 worker는 무시한다.
func (r *workerRegistry) touch(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[id]; ok {
		w.LastSeen = time.Now()
	}
}

// list는 TTL 내에 응답한 활성 worker 목록을 반환한다.
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

// cleanup은 만료된 worker를 제거한다. 주기적으로 호출해야 한다.
func (r *workerRegistry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-workerTTL)
	for id, w := range r.workers {
		if !w.LastSeen.After(cutoff) {
			delete(r.workers, id)
		}
	}
}
