package worker

import (
	"testing"
	"time"

	"github.com/piper/piper/internal/agent"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(Info{ID: "w1", Label: "gpu", Status: StatusOnline})

	workers := r.List()
	if len(workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(workers))
	}
	if workers[0].ID != "w1" {
		t.Errorf("ID = %q, want w1", workers[0].ID)
	}
	if workers[0].Status != StatusOnline {
		t.Errorf("Status = %q, want %q", workers[0].Status, StatusOnline)
	}
}

func TestRegistry_HeartbeatUpdatesInFlight(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(Info{ID: "w1"})

	if err := r.Heartbeat("w1", 3); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	workers := r.List()
	if len(workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(workers))
	}
	if workers[0].InFlight != 3 {
		t.Errorf("InFlight = %d, want 3", workers[0].InFlight)
	}
}

func TestRegistry_Heartbeat_UnknownWorker(t *testing.T) {
	r := NewRegistry(nil)
	if err := r.Heartbeat("unknown", 0); err == nil {
		t.Error("expected error for unregistered worker, got nil")
	}
}

func TestRegistry_TouchUpdatesLastSeen(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(Info{ID: "w1"})

	before := time.Now()
	time.Sleep(time.Millisecond)
	r.Touch("w1")

	workers := r.List()
	if len(workers) != 1 {
		t.Fatalf("expected 1 worker after Touch")
	}
	if !workers[0].LastSeen.After(before) {
		t.Error("LastSeen not updated after Touch")
	}
}

func TestRegistry_Touch_EmptyID(t *testing.T) {
	r := NewRegistry(nil)
	r.Touch("") // must not panic
}

func TestRegistry_Cleanup_RemovesExpired(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(Info{ID: "w1"})

	// Manually backdate LastSeen beyond TTL.
	r.mu.Lock()
	r.workers["w1"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	r.Cleanup()

	if len(r.List()) != 0 {
		t.Error("expired worker should have been removed by Cleanup")
	}
}

func TestRegistry_Cleanup_KeepsActive(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(Info{ID: "w1"})
	r.Register(Info{ID: "w2"})

	// Expire only w2.
	r.mu.Lock()
	r.workers["w2"].LastSeen = time.Now().Add(-(workerTTL + time.Second))
	r.mu.Unlock()

	r.Cleanup()

	workers := r.List()
	if len(workers) != 1 {
		t.Fatalf("expected 1 active worker, got %d", len(workers))
	}
	if workers[0].ID != "w1" {
		t.Errorf("expected w1 to survive, got %q", workers[0].ID)
	}
}

func TestRegistry_ReRegister_UpdatesInfo(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(Info{ID: "w1", Label: "old"})
	r.Register(Info{ID: "w1", Label: "new"})

	workers := r.List()
	if len(workers) != 1 {
		t.Fatalf("re-register should not create duplicate, got %d", len(workers))
	}
	if workers[0].Label != "new" {
		t.Errorf("Label = %q, want new", workers[0].Label)
	}
}

func TestRegistryMirrorsPipelineWorkerToAgentRegistry(t *testing.T) {
	agents := agent.NewRegistry()
	r := NewRegistry(nil)
	r.SetAgentRegistry(agents)

	r.Register(Info{ID: "w1", Label: "gpu", Hostname: "node-1"})

	got, err := agents.Get("w1")
	if err != nil {
		t.Fatalf("agent not registered: %v", err)
	}
	if got.Kind != agent.KindBareMetal {
		t.Fatalf("kind = %q, want %q", got.Kind, agent.KindBareMetal)
	}
	if got.Labels["label"] != "gpu" {
		t.Fatalf("label = %q, want gpu", got.Labels["label"])
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != agent.CapabilityPipeline {
		t.Fatalf("capabilities = %#v, want pipeline", got.Capabilities)
	}
}
