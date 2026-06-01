package agent

import "testing"

func TestRouterSelectExplicitWorker(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Capabilities: []string{CapabilityPipeline}})
	router := NewRouter(reg)

	got, err := router.Select(WorkloadPipeline, Placement{WorkerID: "a1"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "a1" {
		t.Fatalf("worker ID = %q, want a1", got.ID)
	}
}

func TestRouterSelectExplicitWorkerByHostname(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Hostname: "node-a", Capabilities: []string{CapabilityServing}})
	router := NewRouter(reg)

	got, err := router.Select(WorkloadServing, Placement{WorkerID: "node-a"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "a1" {
		t.Fatalf("worker ID = %q, want a1", got.ID)
	}
}

func TestRouterRejectsAmbiguousPlacement(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "a2", Capabilities: []string{CapabilityPipeline}})
	router := NewRouter(reg)

	if _, err := router.Select(WorkloadPipeline, Placement{}); err == nil {
		t.Fatal("expected ambiguous placement error")
	}
}

func TestRouterSelectsCluster(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Kind: KindK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "a2", Kind: KindK8s, ClusterName: "gpu-b", Capabilities: []string{CapabilityPipeline}})
	router := NewRouter(reg)

	got, err := router.Select(WorkloadPipeline, Placement{ClusterName: "gpu-b"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "a2" {
		t.Fatalf("worker ID = %q, want a2", got.ID)
	}
}

func TestRegistryKeepsOneActiveK8sAgentPerCluster(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "old", Kind: KindK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "new", Kind: KindK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})

	if _, err := reg.Get("old"); err == nil {
		t.Fatal("old agent should have been replaced")
	}
	got, err := reg.Get("new")
	if err != nil {
		t.Fatalf("new agent unavailable: %v", err)
	}
	if got.ClusterName != "gpu-a" {
		t.Fatalf("cluster = %q, want gpu-a", got.ClusterName)
	}
}
