package piper

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/piper/piper/pkg/store"
)

const workerTTL = 30 * time.Second

// WorkerInfo는 API 레이어에서 사용하는 Worker 정보 (인메모리 캐시)
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
	store   *store.Store // nil이면 DB 비사용 (임베디드 모드)
}

func newWorkerRegistry(st *store.Store) *workerRegistry {
	return &workerRegistry{
		workers: make(map[string]*WorkerInfo),
		store:   st,
	}
}

// register는 Worker를 등록한다. 같은 ID로 재등록하면 갱신한다.
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

// heartbeat는 Worker의 LastSeen과 InFlight를 갱신한다.
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

// touch는 폴링 요청마다 LastSeen을 갱신한다. 미등록 Worker는 무시한다.
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

// list는 TTL 내에 응답한 활성 Worker 목록을 반환한다.
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

// cleanup은 만료된 Worker를 인메모리에서 제거하고 DB에 offline으로 표시한다.
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
