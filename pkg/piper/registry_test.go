package piper

import (
	"testing"
	"time"
)

func TestRegistry_register_and_list(t *testing.T) {
	r := newWorkerRegistry()
	r.register(WorkerInfo{ID: "w1", Label: "gpu", Concurrency: 2, Hostname: "host1"})
	r.register(WorkerInfo{ID: "w2", Label: "cpu", Concurrency: 4, Hostname: "host2"})

	list := r.list()
	if len(list) != 2 {
		t.Fatalf("want 2 workers, got %d", len(list))
	}
}

func TestRegistry_heartbeat_ok(t *testing.T) {
	r := newWorkerRegistry()
	r.register(WorkerInfo{ID: "w1"})

	if err := r.heartbeat("w1"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegistry_heartbeat_unknown(t *testing.T) {
	r := newWorkerRegistry()
	if err := r.heartbeat("nobody"); err == nil {
		t.Error("expected error for unknown worker")
	}
}

func TestRegistry_touch_updates_last_seen(t *testing.T) {
	r := newWorkerRegistry()
	r.register(WorkerInfo{ID: "w1"})

	before := r.workers["w1"].LastSeen
	time.Sleep(10 * time.Millisecond)
	r.touch("w1")

	if !r.workers["w1"].LastSeen.After(before) {
		t.Error("touch did not update LastSeen")
	}
}

func TestRegistry_touch_unknown_ignored(t *testing.T) {
	r := newWorkerRegistry()
	r.touch("ghost") // 패닉 없이 무시
}

func TestRegistry_list_excludes_expired(t *testing.T) {
	r := newWorkerRegistry()
	r.register(WorkerInfo{ID: "w1"})

	// LastSeen을 TTL보다 오래 전으로 강제 설정
	r.mu.Lock()
	r.workers["w1"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	if len(r.list()) != 0 {
		t.Error("expired worker should not appear in list")
	}
}

func TestRegistry_cleanup_removes_expired(t *testing.T) {
	r := newWorkerRegistry()
	r.register(WorkerInfo{ID: "alive"})
	r.register(WorkerInfo{ID: "dead"})

	r.mu.Lock()
	r.workers["dead"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	r.cleanup()

	r.mu.Lock()
	_, aliveOK := r.workers["alive"]
	_, deadOK := r.workers["dead"]
	r.mu.Unlock()

	if !aliveOK {
		t.Error("alive worker should remain after cleanup")
	}
	if deadOK {
		t.Error("dead worker should be removed by cleanup")
	}
}

func TestRegistry_reregister_updates_info(t *testing.T) {
	r := newWorkerRegistry()
	r.register(WorkerInfo{ID: "w1", Label: "old"})
	r.register(WorkerInfo{ID: "w1", Label: "new"})

	list := r.list()
	if len(list) != 1 {
		t.Fatalf("want 1, got %d", len(list))
	}
	if list[0].Label != "new" {
		t.Errorf("want label 'new', got %q", list[0].Label)
	}
}
