package notebook

import (
	"testing"
	"time"

	"github.com/piper/piper/internal/agent"
)

func TestNotebookWorkerRegistry_RegisterAndPick(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}

	info := &NotebookWorkerInfo{
		ID:       "nb-worker-1",
		Addr:     "http://10.0.0.1:7701",
		Hostname: "node-1",
	}
	r.Register(info)

	got, err := r.Pick()
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if got.ID != "nb-worker-1" {
		t.Errorf("Pick() ID = %q, want %q", got.ID, "nb-worker-1")
	}
}

func TestNotebookWorkerRegistry_HeartbeatUpdatesLastSeen(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}

	info := &NotebookWorkerInfo{ID: "nb-worker-hb", Addr: "http://10.0.0.2:7701"}
	r.Register(info)

	r.mu.Lock()
	r.workers["nb-worker-hb"].LastSeen = time.Now().Add(-30 * time.Second)
	beforeHB := r.workers["nb-worker-hb"].LastSeen
	r.mu.Unlock()

	r.Heartbeat("nb-worker-hb")

	r.mu.RLock()
	afterHB := r.workers["nb-worker-hb"].LastSeen
	r.mu.RUnlock()

	if !afterHB.After(beforeHB) {
		t.Errorf("Heartbeat() did not update LastSeen: before=%v after=%v", beforeHB, afterHB)
	}
}

func TestNotebookWorkerRegistry_HeartbeatUnknownWorker(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}
	// Should not panic for unknown worker.
	r.Heartbeat("nonexistent")
}

func TestNotebookWorkerRegistry_PickEmpty(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}
	_, err := r.Pick()
	if err == nil {
		t.Fatal("Pick() expected error when no workers registered")
	}
}

func TestNotebookWorkerRegistry_RemoveThenPick(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}

	info := &NotebookWorkerInfo{ID: "nb-worker-rm", Addr: "http://10.0.0.3:7701"}
	r.Register(info)
	r.Remove("nb-worker-rm")

	_, err := r.Pick()
	if err == nil {
		t.Fatal("Pick() expected error after Remove()")
	}
}

func TestNotebookWorkerRegistry_ExpiredWorkerExcludedFromPick(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}

	info := &NotebookWorkerInfo{ID: "nb-worker-old", Addr: "http://10.0.0.4:7701"}
	r.Register(info)

	r.mu.Lock()
	r.workers["nb-worker-old"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	_, err := r.Pick()
	if err == nil {
		t.Fatal("Pick() expected error: expired worker should be excluded")
	}
}

func TestNotebookWorkerRegistry_FreshWorkerPickedDespiteExpiredOne(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}

	old := &NotebookWorkerInfo{ID: "nb-worker-old2", Addr: "http://10.0.0.5:7701"}
	r.Register(old)
	r.mu.Lock()
	r.workers["nb-worker-old2"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	fresh := &NotebookWorkerInfo{ID: "nb-worker-fresh", Addr: "http://10.0.0.6:7701"}
	r.Register(fresh)

	got, err := r.Pick()
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if got.ID != "nb-worker-fresh" {
		t.Errorf("Pick() returned %q, want %q", got.ID, "nb-worker-fresh")
	}
}

func TestNotebookWorkerRegistry_List(t *testing.T) {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}

	r.Register(&NotebookWorkerInfo{ID: "w1", Addr: "http://10.0.0.7:7701"})
	r.Register(&NotebookWorkerInfo{ID: "w2", Addr: "http://10.0.0.8:7701"})

	list := r.List()
	if len(list) != 2 {
		t.Errorf("List() len = %d, want 2", len(list))
	}
}

func TestNotebookWorkerRegistryMirrorsToAgentRegistry(t *testing.T) {
	agents := agent.NewRegistry()
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}
	r.SetAgentRegistry(agents)

	r.Register(&NotebookWorkerInfo{ID: "nb1", Addr: "http://node:7701", Hostname: "node", GPUs: []string{"0"}})

	got, err := agents.Get("nb1")
	if err != nil {
		t.Fatalf("agent not registered: %v", err)
	}
	if got.Addr != "http://node:7701" {
		t.Fatalf("addr = %q", got.Addr)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != agent.CapabilityNotebook {
		t.Fatalf("capabilities = %#v, want notebook", got.Capabilities)
	}
}
