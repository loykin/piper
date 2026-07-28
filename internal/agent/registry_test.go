package agent

import (
	"testing"
	"time"
)

func TestRegistryPreservesDockerInfrastructureKind(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "docker-1", Infrastructure: InfrastructureDocker, Capabilities: []string{CapabilityNotebook}})
	got, err := reg.Get("docker-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Infrastructure != InfrastructureDocker {
		t.Fatalf("infrastructure = %q, want docker", got.Infrastructure)
	}
}

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

// TestRouterSelectRespectsExplicitRuntimeAmongMixedInfrastructure reproduces
// a workload declaring driver.placement.runtime="docker" while both a k8s
// and a docker worker advertise the same capability (e.g. "notebook").
// Without an infrastructure filter, auto-assign picks whichever worker wins
// the load tiebreak — which can silently be the k8s one — and the declared
// runtime is never honored.
func TestRouterSelectRespectsExplicitRuntimeAmongMixedInfrastructure(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "k8s-worker", Infrastructure: InfrastructureK8s, Capabilities: []string{CapabilityNotebook}})
	reg.Register(Info{ID: "docker-worker", Infrastructure: InfrastructureDocker, Capabilities: []string{CapabilityNotebook}})
	router := NewRouter(reg)

	got, err := router.Select(WorkloadNotebook, Placement{Infrastructure: InfrastructureDocker})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "docker-worker" {
		t.Fatalf("selected worker = %q, want docker-worker", got.ID)
	}

	got, err = router.Select(WorkloadNotebook, Placement{Infrastructure: InfrastructureK8s})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "k8s-worker" {
		t.Fatalf("selected worker = %q, want k8s-worker", got.ID)
	}

	if _, err := router.Select(WorkloadNotebook, Placement{Infrastructure: InfrastructureBaremetal}); err == nil {
		t.Fatal("expected no baremetal candidate to be available")
	}
}

// TestRouterSelectRejectsExplicitWorkerWithMismatchedRuntime ensures that
// even an explicit worker_id is rejected if it contradicts a declared
// driver.placement.runtime, mirroring RequireContainer's behavior.
func TestRouterSelectRejectsExplicitWorkerWithMismatchedRuntime(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "k8s-worker", Infrastructure: InfrastructureK8s, Capabilities: []string{CapabilityNotebook}})
	router := NewRouter(reg)

	if _, err := router.Select(WorkloadNotebook, Placement{
		WorkerID:       "k8s-worker",
		Infrastructure: InfrastructureDocker,
	}); err == nil {
		t.Fatal("expected explicit k8s worker to reject a docker-runtime placement")
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
		ID:             "baremetal",
		Infrastructure: InfrastructureBaremetal,
		Capabilities:   []string{CapabilityPipeline},
	})
	reg.Register(Info{
		ID:             "docker",
		Infrastructure: InfrastructureDocker,
		Capabilities:   []string{CapabilityPipeline},
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

func TestRouterReserveRequiresAdditionalCapability(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "pipeline-only", Capabilities: []string{CapabilityPipeline}, Capacity: 2})
	reg.Register(Info{ID: "notebook-ready", Capabilities: []string{CapabilityPipeline, CapabilityNotebook}, Capacity: 2})
	router := NewRouter(reg)

	got, err := router.Reserve(WorkloadPipeline, Placement{RequiredCapabilities: []string{CapabilityNotebook}})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "notebook-ready" {
		t.Fatalf("selected worker = %q, want notebook-ready", got.ID)
	}
	explicit, err := router.Reserve(WorkloadPipeline, Placement{
		WorkerID:             "pipeline-only",
		RequiredCapabilities: []string{CapabilityNotebook},
	})
	if err != nil {
		t.Fatalf("explicit worker override: %v", err)
	}
	if explicit.ID != "pipeline-only" {
		t.Fatalf("explicit worker = %q, want pipeline-only", explicit.ID)
	}
}

func TestRegistryListHasStableRegistrationOrder(t *testing.T) {
	reg := NewRegistry()
	base := time.Now()
	reg.Register(Info{ID: "later", RegisteredAt: base.Add(time.Second)})
	reg.Register(Info{ID: "first-b", RegisteredAt: base})
	reg.Register(Info{ID: "first-a", RegisteredAt: base})

	got := reg.List()
	want := []string{"first-a", "first-b", "later"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("List()[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestRouterSelectsCluster(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "a1", Infrastructure: InfrastructureK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "a2", Infrastructure: InfrastructureK8s, ClusterName: "gpu-b", Capabilities: []string{CapabilityPipeline}})
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
	reg.Register(Info{ID: "old", Infrastructure: InfrastructureK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "new", Infrastructure: InfrastructureK8s, ClusterName: "gpu-a", Capabilities: []string{CapabilityPipeline}})

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
