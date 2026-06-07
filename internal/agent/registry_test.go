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

func TestRouterSelectsOneFromMultipleCandidates(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Capabilities: []string{CapabilityPipeline}, Capacity: 4})
	reg.Register(Info{ID: "a2", Capabilities: []string{CapabilityPipeline}, Capacity: 4})
	router := NewRouter(reg)

	// With multiple matching agents, router should pick one (not error).
	got, err := router.Select(WorkloadPipeline, Placement{})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "a1" && got.ID != "a2" {
		t.Fatalf("unexpected worker ID %q", got.ID)
	}
}

func TestRouterReserveUsesLeastLoadedAgent(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Capabilities: []string{CapabilityPipeline}, Capacity: 2})
	reg.Register(Info{ID: "a2", Capabilities: []string{CapabilityPipeline}, Capacity: 2})
	router := NewRouter(reg)

	first, err := router.Reserve(WorkloadPipeline, Placement{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := router.Reserve(WorkloadPipeline, Placement{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("two reservations used the same agent %q while another agent was idle", first.ID)
	}

	router.Release(first.ID)
	third, err := router.Reserve(WorkloadPipeline, Placement{})
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != first.ID {
		t.Fatalf("released agent = %q, next reservation = %q", first.ID, third.ID)
	}
}

func TestRouterReserveRejectsFullBoundedAgent(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Capabilities: []string{CapabilityPipeline}, Capacity: 1})
	router := NewRouter(reg)

	if _, err := router.Reserve(WorkloadPipeline, Placement{}); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Reserve(WorkloadPipeline, Placement{}); err == nil {
		t.Fatal("expected full agent reservation to fail")
	}
}

func TestRouterReserveImagePipelineRequiresContainerRuntime(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{
		ID:           "baremetal",
		Runtime:      RuntimeBaremetal,
		Capabilities: []string{CapabilityPipeline},
	})
	reg.Register(Info{
		ID:           "docker",
		Runtime:      RuntimeDocker,
		Capabilities: []string{CapabilityPipeline},
	})
	router := NewRouter(reg)

	got, err := router.Reserve(WorkloadPipeline, Placement{RequireContainer: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "docker" {
		t.Fatalf("selected worker = %q, want docker", got.ID)
	}
	if _, err := router.Reserve(WorkloadPipeline, Placement{
		WorkerID:         "baremetal",
		RequireContainer: true,
	}); err == nil {
		t.Fatal("expected explicit baremetal worker to reject image pipeline")
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
