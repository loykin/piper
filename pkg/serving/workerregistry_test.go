package serving

import (
	"testing"
	"time"
)

func TestServingWorkerRegistry_RegisterAndPick(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}

	info := &ServingWorkerInfo{
		ID:       "worker-1",
		Addr:     "http://10.0.0.1:7700",
		Hostname: "node-1",
	}
	r.Register(info)

	got, err := r.Pick()
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if got.ID != "worker-1" {
		t.Errorf("Pick() ID = %q, want %q", got.ID, "worker-1")
	}
}

func TestServingWorkerRegistry_HeartbeatUpdatesLastSeen(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}

	info := &ServingWorkerInfo{ID: "worker-hb", Addr: "http://10.0.0.2:7700"}
	r.Register(info)

	// Manually backdate LastSeen to verify Heartbeat refreshes it.
	r.mu.Lock()
	r.workers["worker-hb"].LastSeen = time.Now().Add(-30 * time.Second)
	beforeHB := r.workers["worker-hb"].LastSeen
	r.mu.Unlock()

	r.Heartbeat("worker-hb")

	r.mu.RLock()
	afterHB := r.workers["worker-hb"].LastSeen
	r.mu.RUnlock()

	if !afterHB.After(beforeHB) {
		t.Errorf("Heartbeat() did not update LastSeen: before=%v after=%v", beforeHB, afterHB)
	}
}

func TestServingWorkerRegistry_HeartbeatUnknownWorker(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}
	// Should not panic for unknown worker.
	r.Heartbeat("nonexistent")
}

func TestServingWorkerRegistry_PickEmpty(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}
	_, err := r.Pick()
	if err == nil {
		t.Fatal("Pick() expected error when no workers registered")
	}
}

func TestServingWorkerRegistry_RemoveThenPick(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}

	info := &ServingWorkerInfo{ID: "worker-rm", Addr: "http://10.0.0.3:7700"}
	r.Register(info)
	r.Remove("worker-rm")

	_, err := r.Pick()
	if err == nil {
		t.Fatal("Pick() expected error after Remove()")
	}
}

func TestServingWorkerRegistry_ExpiredWorkerExcludedFromPick(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}

	// Register a worker then manually set its LastSeen to beyond TTL.
	info := &ServingWorkerInfo{ID: "worker-old", Addr: "http://10.0.0.4:7700"}
	r.Register(info)

	r.mu.Lock()
	r.workers["worker-old"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	_, err := r.Pick()
	if err == nil {
		t.Fatal("Pick() expected error: expired worker should be excluded")
	}
}

func TestServingWorkerRegistry_FreshWorkerPickedDespiteExpiredOne(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}

	// Register an expired worker.
	old := &ServingWorkerInfo{ID: "worker-old2", Addr: "http://10.0.0.5:7700"}
	r.Register(old)
	r.mu.Lock()
	r.workers["worker-old2"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	// Register a fresh worker.
	fresh := &ServingWorkerInfo{ID: "worker-fresh", Addr: "http://10.0.0.6:7700"}
	r.Register(fresh)

	got, err := r.Pick()
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if got.ID != "worker-fresh" {
		t.Errorf("Pick() returned %q, want %q", got.ID, "worker-fresh")
	}
}

func TestServingWorkerRegistry_List(t *testing.T) {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}

	r.Register(&ServingWorkerInfo{ID: "w1", Addr: "http://10.0.0.7:7700"})
	r.Register(&ServingWorkerInfo{ID: "w2", Addr: "http://10.0.0.8:7700"})

	list := r.List()
	if len(list) != 2 {
		t.Errorf("List() len = %d, want 2", len(list))
	}
}
