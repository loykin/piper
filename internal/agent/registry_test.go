package agent

import (
	"errors"
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

// TestRouterSelectRespectsNamespaceAllowList reproduces a k8s worker
// registered with a restricted Namespaces allow-list (as sent over the gRPC
// registration protocol). A placement targeting a namespace outside that
// list must not be routed to it, and one inside it must succeed.
func TestRouterSelectRespectsNamespaceAllowList(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{
		ID:             "k8s-worker",
		Infrastructure: InfrastructureK8s,
		Capabilities:   []string{CapabilityNotebook},
		Namespaces:     []string{"piper"},
	})
	router := NewRouter(reg)

	if _, err := router.Select(WorkloadNotebook, Placement{Namespace: "notebooks"}); err == nil {
		t.Fatal("expected no candidate for a namespace outside the worker's allow-list")
	}

	got, err := router.Select(WorkloadNotebook, Placement{Namespace: "piper"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "k8s-worker" {
		t.Fatalf("selected worker = %q, want k8s-worker", got.ID)
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

// TestRouterSelectRejectsAmbiguousMixedInfrastructure reproduces the live
// bug found during adversarial QA (2026-08-02): with a baremetal and a
// docker pipeline worker both registered, a task with no
// driver.placement.runtime and no image (RequireContainer=false) used to be
// silently load-balanced across them by selectLeastLoaded — non-deterministically
// landing on the docker worker, where it fails immediately with "no docker
// image configured". Select must now refuse to guess and return
// AmbiguousInfrastructureError instead.
func TestRouterSelectRejectsAmbiguousMixedInfrastructure(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "bm", Infrastructure: InfrastructureBaremetal, Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "dk", Infrastructure: InfrastructureDocker, Capabilities: []string{CapabilityPipeline}})
	router := NewRouter(reg)

	_, err := router.Select(WorkloadPipeline, Placement{})
	if err == nil {
		t.Fatal("expected an error for an unset placement across mixed infrastructure types")
	}
	var ambiguous *AmbiguousInfrastructureError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %v (%T), want *AmbiguousInfrastructureError", err, err)
	}
	if len(ambiguous.Types) != 2 {
		t.Fatalf("ambiguous.Types = %v, want 2 entries", ambiguous.Types)
	}
}

// TestRouterReserveRejectsAmbiguousMixedInfrastructure is the Reserve()
// counterpart — the atomic reserve-a-slot path used by pipeline dispatch
// must apply the same check, not just the read-only Select() path.
func TestRouterReserveRejectsAmbiguousMixedInfrastructure(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "bm", Infrastructure: InfrastructureBaremetal, Capabilities: []string{CapabilityPipeline}, Capacity: 4})
	reg.Register(Info{ID: "dk", Infrastructure: InfrastructureDocker, Capabilities: []string{CapabilityPipeline}, Capacity: 4})
	router := NewRouter(reg)

	_, err := router.Reserve(WorkloadPipeline, Placement{})
	var ambiguous *AmbiguousInfrastructureError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %v (%T), want *AmbiguousInfrastructureError", err, err)
	}

	// Reserve must not have side-effected any reservation on either
	// candidate when it bails out ambiguous — a later, correctly-disambiguated
	// call must still see full capacity on both.
	got, err := router.Reserve(WorkloadPipeline, Placement{Infrastructure: InfrastructureBaremetal})
	if err != nil {
		t.Fatalf("Reserve with explicit runtime failed: %v", err)
	}
	if got.ID != "bm" {
		t.Fatalf("selected worker = %q, want bm", got.ID)
	}
}

// TestRouterSelectAllowsMultipleWorkersOfSameInfrastructure ensures the
// ambiguity check is scoped to *different* infrastructure types, not just
// "more than one candidate" — N interchangeable workers of the same type
// must keep load-balancing exactly as before.
func TestRouterSelectAllowsMultipleWorkersOfSameInfrastructure(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "bm1", Infrastructure: InfrastructureBaremetal, Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "bm2", Infrastructure: InfrastructureBaremetal, Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "bm3", Infrastructure: InfrastructureBaremetal, Capabilities: []string{CapabilityPipeline}})
	router := NewRouter(reg)

	got, err := router.Select(WorkloadPipeline, Placement{})
	if err != nil {
		t.Fatalf("Select returned error for same-infrastructure candidates: %v", err)
	}
	if got.ID != "bm1" && got.ID != "bm2" && got.ID != "bm3" {
		t.Fatalf("unexpected worker ID %q", got.ID)
	}
}

// TestRouterSelectExplicitRuntimeResolvesMixedInfrastructure confirms that
// setting placement.Infrastructure (driver.placement.runtime) is still all
// that's needed to disambiguate — the new check only fires when it's unset.
func TestRouterSelectExplicitRuntimeResolvesMixedInfrastructure(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{ID: "bm", Infrastructure: InfrastructureBaremetal, Capabilities: []string{CapabilityPipeline}})
	reg.Register(Info{ID: "dk", Infrastructure: InfrastructureDocker, Capabilities: []string{CapabilityPipeline}})
	router := NewRouter(reg)

	got, err := router.Select(WorkloadPipeline, Placement{Infrastructure: InfrastructureDocker})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.ID != "dk" {
		t.Fatalf("selected worker = %q, want dk", got.ID)
	}
}
