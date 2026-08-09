package pipelinedispatch

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
)

// stubPipelinePolicyRepo is a minimal WorkerPodPolicyRepository for pipeline tests.
type stubPipelinePolicyRepo struct {
	policies map[string]*iagent.WorkerPodPolicy
}

func newStubPipelinePolicyRepo(entries ...*iagent.WorkerPodPolicy) *stubPipelinePolicyRepo {
	r := &stubPipelinePolicyRepo{policies: make(map[string]*iagent.WorkerPodPolicy)}
	for _, e := range entries {
		r.policies[e.WorkerID] = e
	}
	return r
}

func (r *stubPipelinePolicyRepo) List(_ context.Context) ([]iagent.WorkerPodPolicy, error) {
	return nil, nil
}

func (r *stubPipelinePolicyRepo) Get(_ context.Context, workerID string) (*iagent.WorkerPodPolicy, error) {
	return r.policies[workerID], nil
}

func (r *stubPipelinePolicyRepo) Set(_ context.Context, p iagent.WorkerPodPolicy) error {
	r.policies[p.WorkerID] = &p
	return nil
}

func (r *stubPipelinePolicyRepo) Delete(_ context.Context, workerID string) error {
	delete(r.policies, workerID)
	return nil
}

type recordingPipelineAgentRPC struct {
	mu      sync.Mutex
	calls   []pipelineAgentRPCCall
	sendErr error
}

type pipelineAgentRPCCall struct {
	AgentID string
	Method  string
	Payload any
}

func (r *recordingPipelineAgentRPC) SendRPC(_ context.Context, agentID, method string, payload any, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, pipelineAgentRPCCall{AgentID: agentID, Method: method, Payload: payload})
	return r.sendErr
}

func (r *recordingPipelineAgentRPC) snapshot() []pipelineAgentRPCCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pipelineAgentRPCCall(nil), r.calls...)
}

func TestAgentBackendDispatchUsesPipelinePlacement(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "agent-1",
		Infrastructure: iagent.InfrastructureK8s,
		Labels:         map[string]string{"label": "gpu"},
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Label: "gpu"}}}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	calls := rpc.snapshot()
	if calls[0].AgentID != "agent-1" {
		t.Fatalf("agent id = %q", calls[0].AgentID)
	}
	if calls[0].Method != iagent.MethodPipelineDispatch {
		t.Fatalf("method = %q", calls[0].Method)
	}
}

// TestAgentBackendDispatchRespectsExplicitRuntimeAmongMixedInfrastructure
// reproduces a pipeline declaring driver.placement.runtime="docker" while a
// k8s worker is registered first and a docker worker second, both
// advertising the pipeline capability. Without an infrastructure filter,
// taskPlacement never forwards the declared runtime and the router can pick
// the k8s worker, silently ignoring the explicit runtime selection.
func TestAgentBackendDispatchRespectsExplicitRuntimeAmongMixedInfrastructure(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "k8s-agent",
		Infrastructure: iagent.InfrastructureK8s,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	reg.Register(iagent.Info{
		ID:             "docker-agent",
		Infrastructure: iagent.InfrastructureDocker,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Runtime: iagent.InfrastructureDocker}}}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	calls := rpc.snapshot()
	if calls[0].AgentID != "docker-agent" {
		t.Fatalf("agent id = %q, want docker-agent", calls[0].AgentID)
	}
}

// TestAgentBackendDispatchRejectsAmbiguousInfrastructureAsNonRetryable
// reproduces the live bug found during adversarial QA (2026-08-02): a
// pipeline with no declared driver.placement.runtime, dispatched while a
// baremetal and a docker worker are both registered, must fail clearly and
// permanently instead of being silently (and non-deterministically)
// load-balanced onto a worker it was never configured to run on. Marking it
// Retryable would make the queue retry forever without ever succeeding,
// since nothing about the pipeline changes between attempts.
func TestAgentBackendDispatchRejectsAmbiguousInfrastructureAsNonRetryable(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "bm-agent",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	reg.Register(iagent.Info{
		ID:             "docker-agent",
		Infrastructure: iagent.InfrastructureDocker,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pl := pipeline.Pipeline{}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	err := backend.Dispatch(context.Background(), task)
	if err == nil {
		t.Fatal("expected Dispatch to fail for unset placement across mixed infrastructure types")
	}
	var de *DispatchError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *DispatchError", err, err)
	}
	if de.Retryable {
		t.Fatal("ambiguous-infrastructure dispatch failure must not be marked Retryable — it will never resolve on its own")
	}
	if len(rpc.snapshot()) != 0 {
		t.Fatal("no RPC should have been sent for a rejected ambiguous dispatch")
	}
}

func TestAgentBackendDispatchPreservesTaskEnv(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{
		ID:       "run-1:clone",
		RunID:    "run-1",
		Pipeline: pipelineJSON,
		Env:      []string{"PIPER_GIT_TOKEN=tok", "PIPER_GIT_USER=user"},
	}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	calls := rpc.snapshot()
	sentTask, ok := calls[0].Payload.(*proto.Task)
	if !ok {
		t.Fatalf("payload type = %T", calls[0].Payload)
	}
	if got := sentTask.Env; len(got) != 2 || got[0] != "PIPER_GIT_TOKEN=tok" || got[1] != "PIPER_GIT_USER=user" {
		t.Fatalf("env = %#v", got)
	}
}

func TestAgentBackendCancelUsesDispatchAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Infrastructure: iagent.InfrastructureK8s, Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	calls := rpc.snapshot()
	if calls[1].Method != iagent.MethodPipelineCancelRun {
		t.Fatalf("method = %q", calls[1].Method)
	}
}

func TestAgentBackendCancelCarriesPipelineNamespace(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Infrastructure: iagent.InfrastructureK8s, Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{}, K8s: &manifest.DriverK8sSpec{Namespace: "runs"}}}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	payload := rpc.snapshot()[1].Payload.(map[string]any)
	if payload["namespace"] != "runs" {
		t.Fatalf("namespace = %q, want runs", payload["namespace"])
	}
}

func TestAgentBackendPinsAllRunStepsToOneAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	reg.Register(iagent.Info{ID: "agent-2", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	// Two identically-capable agents are registered; the pipeline must name
	// one explicitly (driver.placement.worker) since the router now refuses
	// to guess between them.
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{Spec: pipeline.PipelineSpec{
		Defaults: &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "agent-1"}},
		},
	}})

	tasks := []*proto.Task{
		{ID: "run-1:first", RunID: "run-1", Pipeline: pipelineJSON},
		{ID: "run-1:second", RunID: "run-1", Pipeline: pipelineJSON},
	}
	for _, task := range tasks {
		if err := backend.Dispatch(context.Background(), task); err != nil {
			t.Fatalf("Dispatch(%s) returned error: %v", task.ID, err)
		}
	}

	calls := rpc.snapshot()
	if len(calls) != 2 {
		t.Fatalf("dispatch calls = %d, want 2", len(calls))
	}
	if calls[0].AgentID != calls[1].AgentID {
		t.Fatalf("run dispatched to multiple agents: %q and %q", calls[0].AgentID, calls[1].AgentID)
	}
}

func TestAgentBackendPinsConcurrentRunStepsToOneAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	reg.Register(iagent.Info{ID: "agent-2", Capabilities: []string{iagent.CapabilityPipeline}, Capacity: 4})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	// See TestAgentBackendPinsAllRunStepsToOneAgent: an explicit worker is
	// required now that the router refuses to guess among same-type peers.
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{Spec: pipeline.PipelineSpec{
		Defaults: &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "agent-1"}},
		},
	}})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, step := range []string{"first", "second"} {
		wg.Add(1)
		go func(step string) {
			defer wg.Done()
			errs <- backend.Dispatch(context.Background(), &proto.Task{
				ID:       "run-1:" + step,
				RunID:    "run-1",
				Pipeline: pipelineJSON,
			})
		}(step)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Dispatch returned error: %v", err)
		}
	}

	calls := rpc.snapshot()
	if len(calls) != 2 {
		t.Fatalf("dispatch calls = %d, want 2", len(calls))
	}
	if calls[0].AgentID != calls[1].AgentID {
		t.Fatalf("concurrent run dispatch used multiple agents: %q and %q", calls[0].AgentID, calls[1].AgentID)
	}
}

// TestAgentBackendReleasesUncommittedRunBindingAfterBusy confirms that a
// busy dispatch releases its uncommitted run binding so a retry against the
// *same, explicitly named* worker can succeed once it frees up. It does
// NOT retry on a different worker automatically — even when a same-type
// sibling is registered, redirecting there without being asked would be
// exactly the silent "landed on an unintended worker" outcome placement.worker
// exists to prevent. If the named worker becomes unavailable, the retry
// must fail rather than being redirected.
func TestAgentBackendReleasesUncommittedRunBindingAfterBusy(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "agent-1",
		Infrastructure: iagent.InfrastructureDocker,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       1,
	})
	reg.Register(iagent.Info{
		ID:             "agent-2",
		Infrastructure: iagent.InfrastructureDocker,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       1,
	})
	rpc := &recordingPipelineAgentRPC{sendErr: &iagent.BusyError{Reason: "actual worker state is full"}}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{Spec: pipeline.PipelineSpec{
		Defaults: &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "agent-1"}},
		},
	}})
	task := &proto.Task{ID: "run-1:first", RunID: "run-1", Pipeline: pipelineJSON}

	if err := backend.Dispatch(context.Background(), task); err == nil {
		t.Fatal("expected busy dispatch error")
	}
	backend.runMu.Lock()
	_, bound := backend.runAgents[task.RunID]
	backend.runMu.Unlock()
	if bound {
		t.Fatal("run remained bound after every initial dispatch was rejected")
	}

	rpc.mu.Lock()
	rpc.sendErr = nil
	rpc.mu.Unlock()
	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("retry on the same named worker failed: %v", err)
	}
	if got := rpc.snapshot()[1].AgentID; got != "agent-1" {
		t.Fatalf("retry worker = %q, want agent-1 (no silent failover to a sibling)", got)
	}
}

// TestAgentBackendDoesNotFailoverWhenNamedWorkerIsRemoved confirms that if
// the explicitly named worker disappears entirely, a retry fails outright
// instead of silently landing on a same-type sibling.
func TestAgentBackendDoesNotFailoverWhenNamedWorkerIsRemoved(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "agent-1",
		Infrastructure: iagent.InfrastructureDocker,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       1,
	})
	reg.Register(iagent.Info{
		ID:             "agent-2",
		Infrastructure: iagent.InfrastructureDocker,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       1,
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{Spec: pipeline.PipelineSpec{
		Defaults: &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "agent-1"}},
		},
	}})
	task := &proto.Task{ID: "run-1:first", RunID: "run-1", Pipeline: pipelineJSON}

	reg.Remove("agent-1")
	if err := backend.Dispatch(context.Background(), task); err == nil {
		t.Fatal("expected dispatch to fail when the named worker is gone, not redirect to agent-2")
	}
	if len(rpc.snapshot()) != 0 {
		t.Fatalf("dispatch calls = %d, want 0 (must not have silently used agent-2)", len(rpc.snapshot()))
	}
}

func TestTaskPlacementRejectsMultipleRunnerLabels(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Steps = []pipeline.Step{
		{Name: "cpu", Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Label: "cpu"}}},
		{Name: "gpu", Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Label: "gpu"}}},
	}
	pipelineJSON, _ := json.Marshal(pl)

	_, err := taskPlacement(&proto.Task{RunID: "run-1", Pipeline: pipelineJSON})
	if err == nil {
		t.Fatal("expected incompatible runner labels to be rejected")
	}
}

func TestTaskPlacementNotebookStepRequiresNotebookCapability(t *testing.T) {
	pl := pipeline.Pipeline{Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{{
		Name: "train",
		Run:  pipeline.Run{Type: "notebook"},
	}}}}
	pipelineJSON, _ := json.Marshal(pl)

	placement, err := taskPlacement(&proto.Task{RunID: "run-1", Pipeline: pipelineJSON})
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.RequiredCapabilities) != 1 || placement.RequiredCapabilities[0] != iagent.CapabilityNotebook {
		t.Fatalf("required capabilities = %v, want notebook", placement.RequiredCapabilities)
	}
}

// TestTaskPlacementPrefersTaskWorkerIDOverManifest verifies that
// task.WorkerID — set by queue.go's recoverWithEnvLocked from a run's
// persisted binding when the manifest itself never named a worker (the
// auto-assigned case) — takes priority as an explicit placement, so a
// recovered run's remaining steps go back to the same worker instead of
// running router selection from scratch.
func TestTaskPlacementPrefersTaskWorkerIDOverManifest(t *testing.T) {
	pl := pipeline.Pipeline{} // no defaults.driver.placement.worker at all
	pipelineJSON, _ := json.Marshal(pl)

	placement, err := taskPlacement(&proto.Task{RunID: "run-1", Pipeline: pipelineJSON, WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	if placement.WorkerID != "worker-a" {
		t.Fatalf("placement.WorkerID = %q, want %q", placement.WorkerID, "worker-a")
	}
}

// TestTaskPlacementTaskWorkerIDWinsEvenWithManifestPlacement verifies
// task.WorkerID wins even when the manifest *also* declares an explicit
// placement.worker — task.WorkerID is trusted as the already-resolved
// decision, not re-derived from the manifest. In practice these never
// actually disagree: both of task.WorkerID's real construction sites
// (queue.go's addWithEnvLocked for a live dispatch, recoverWithEnvLocked for
// a recovered one) already apply the manifest's own priority themselves
// before task.WorkerID is ever set, so by the time taskPlacement runs, this
// is enforcing a documented invariant rather than resolving a real
// conflict — but taskPlacement must still be correct for any task.WorkerID
// value handed to it, not just the two callers that happen to exist today.
func TestTaskPlacementTaskWorkerIDWinsEvenWithManifestPlacement(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "worker-manifest"}}}
	pipelineJSON, _ := json.Marshal(pl)

	placement, err := taskPlacement(&proto.Task{RunID: "run-1", Pipeline: pipelineJSON, WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	if placement.WorkerID != "worker-a" {
		t.Fatalf("placement.WorkerID = %q, want %q", placement.WorkerID, "worker-a")
	}
}

func TestAgentBackendDispatch_AppliesPodPolicyToDefaults(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "k8s-1",
		Infrastructure: iagent.InfrastructureK8s,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       1,
	})
	rpc := &recordingPipelineAgentRPC{}
	repo := newStubPipelinePolicyRepo(&iagent.WorkerPodPolicy{
		WorkerID: "k8s-1",
		PodTemplate: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"tier": "gpu"},
			},
		},
	})
	b := NewAgentBackend(iagent.NewRouter(reg), rpc, nil, repo)

	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{Image: "train:latest"},
		},
	}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := b.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	calls := rpc.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 RPC call, got %d", len(calls))
	}
	sentTask, ok := calls[0].Payload.(*proto.Task)
	if !ok {
		t.Fatalf("payload type: %T", calls[0].Payload)
	}
	var sent pipeline.Pipeline
	if err := json.Unmarshal(sentTask.Pipeline, &sent); err != nil {
		t.Fatalf("unmarshal sent pipeline: %v", err)
	}
	if sent.Spec.Defaults == nil || sent.Spec.Defaults.Driver.K8s == nil {
		t.Fatal("defaults K8s driver should be present in sent pipeline")
	}
	ns := sent.Spec.Defaults.Driver.K8s.PodTemplate.Spec.NodeSelector
	if ns["tier"] != "gpu" {
		t.Errorf("policy nodeSelector not applied: got %v", ns)
	}
}

func TestAgentBackendDispatch_ManifestWinsOverPolicy(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "k8s-1",
		Infrastructure: iagent.InfrastructureK8s,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       1,
	})
	rpc := &recordingPipelineAgentRPC{}
	repo := newStubPipelinePolicyRepo(&iagent.WorkerPodPolicy{
		WorkerID: "k8s-1",
		PodTemplate: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"tier": "policy"},
			},
		},
	})
	b := NewAgentBackend(iagent.NewRouter(reg), rpc, nil, repo)

	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{
				Image: "train:latest",
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						NodeSelector: map[string]string{"tier": "manifest"},
					},
				},
			},
		},
	}
	pipelineJSON, _ := json.Marshal(pl)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := b.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	calls := rpc.snapshot()
	sentTask := calls[0].Payload.(*proto.Task)
	var sent pipeline.Pipeline
	_ = json.Unmarshal(sentTask.Pipeline, &sent)
	ns := sent.Spec.Defaults.Driver.K8s.PodTemplate.Spec.NodeSelector
	if ns["tier"] != "manifest" {
		t.Errorf("manifest should win over policy: tier=%q (want manifest)", ns["tier"])
	}
}

func TestAgentBackendDispatch_NoPolicyNoChange(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "k8s-1",
		Capabilities: []string{iagent.CapabilityPipeline},
		Capacity:     1,
	})
	rpc := &recordingPipelineAgentRPC{}
	// no policy repo passed → NewAgentBackend with no variadic arg
	b := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}

	if err := b.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	calls := rpc.snapshot()
	sentTask := calls[0].Payload.(*proto.Task)
	// pipeline bytes should be identical — no merge happened
	if string(sentTask.Pipeline) != string(pipelineJSON) {
		t.Errorf("pipeline should be unchanged when no policy repo is configured")
	}
}

// TestCancelRunTombstonesUnboundRun reproduces the race where CancelRun
// arrives before any Dispatch call has bound a run to a worker: without the
// tombstone, CancelRun silently no-ops (returns nil) and a subsequently
// racing Dispatch call still sends the workload to a worker.
func TestCancelRunTombstonesUnboundRun(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "agent-1",
		Capabilities: []string{iagent.CapabilityPipeline},
	})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun on unbound run returned error: %v", err)
	}

	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", Pipeline: pipelineJSON}
	if err := backend.Dispatch(context.Background(), task); err == nil {
		t.Fatal("Dispatch succeeded for a run that was canceled before any binding existed")
	}
	if calls := rpc.snapshot(); len(calls) != 0 {
		t.Fatalf("expected no RPC calls after tombstoned dispatch, got %#v", calls)
	}

	// The tombstone must be consumed so it doesn't leak: a fresh dispatch for
	// a *different* run must not be affected.
	backend.runMu.Lock()
	_, stillTombstoned := backend.canceledRuns["run-1"]
	backend.runMu.Unlock()
	if stillTombstoned {
		t.Fatal("tombstone was not cleaned up after the aborted dispatch consumed it")
	}
}

// TestAgentBackendDispatch_CancelDuringBindingPreventsWorkloadStart
// reproduces a cancel arriving while a run's binding is still in flight
// (its runAgent already exists — created by Dispatch before the DB write —
// but nothing has actually reached the worker yet). Before CancelRun
// treated "runAgent exists" as "already dispatched, send a real cancel
// RPC," this raced: Cancel would find the runAgent, no-op fetch, delete it
// without a tombstone, and the in-flight Dispatch call would then finish
// its binding and proceed to send the workload anyway — starting it after
// the user had already canceled it.
func TestAgentBackendDispatch_CancelDuringBindingPreventsWorkloadStart(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	repo := newStubRunRepoForBinding()
	repo.started = make(chan struct{}, 1)
	repo.gate = make(chan struct{})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, repo)

	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	dispatchDone := make(chan error, 1)
	go func() {
		dispatchDone <- backend.Dispatch(context.Background(), &proto.Task{
			ID: "run-1:train", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON,
		})
	}()

	// Wait until Dispatch has created the runAgent and is blocked inside
	// confirmRunBinding (on repo.gate) before canceling.
	select {
	case <-repo.started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch never reached confirmRunBinding")
	}

	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	// Only now let the binding call finish.
	close(repo.gate)

	select {
	case err := <-dispatchDone:
		if err == nil {
			t.Fatal("Dispatch succeeded for a run that was canceled while its binding was still in flight")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch did not return after the binding gate was opened")
	}

	if calls := rpc.snapshot(); len(calls) != 0 {
		t.Fatalf("an RPC was sent for a run canceled before its dispatch could complete: %#v", calls)
	}
}

// stubRunRepoForBinding is a minimal, in-memory run.Repository stub used
// only to observe/control confirmRunBinding's ordering and conflict
// handling — not a general-purpose test double for the whole interface.
type stubRunRepoForBinding struct {
	mu       sync.Mutex
	bound    map[string]string // "projectID/runID" -> workerID
	setCalls int
	forceErr error

	// started, if non-nil, receives a signal (buffered, size >=1) the
	// instant SetWorkerID is called, before it waits on gate — lets a test
	// deterministically know a binding call has begun without sleeping.
	started chan struct{}
	// gate, if non-nil, blocks SetWorkerID until it's closed — lets a test
	// hold a binding call open to force a concurrent-dispatch race window.
	gate chan struct{}
}

func newStubRunRepoForBinding() *stubRunRepoForBinding {
	return &stubRunRepoForBinding{bound: make(map[string]string)}
}

func runBindingKey(projectID, id string) string { return projectID + "/" + id }

func (r *stubRunRepoForBinding) SetWorkerID(_ context.Context, projectID, id, workerID string) (bool, error) {
	if r.started != nil {
		r.started <- struct{}{}
	}
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setCalls++
	if r.forceErr != nil {
		return false, r.forceErr
	}
	key := runBindingKey(projectID, id)
	if _, exists := r.bound[key]; exists {
		return false, nil
	}
	r.bound[key] = workerID
	return true, nil
}

func (r *stubRunRepoForBinding) Get(_ context.Context, projectID, id string) (*run.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workerID, ok := r.bound[runBindingKey(projectID, id)]
	if !ok {
		return nil, nil
	}
	return &run.Run{ID: id, ProjectID: projectID, WorkerID: workerID}, nil
}

func (r *stubRunRepoForBinding) Create(context.Context, *run.Run) error { return nil }
func (r *stubRunRepoForBinding) List(context.Context, string, run.RunFilter) ([]*run.Run, error) {
	return nil, nil
}
func (r *stubRunRepoForBinding) Count(context.Context, string, run.RunFilter) (int, error) {
	return 0, nil
}
func (r *stubRunRepoForBinding) UpdateStatus(context.Context, string, string, string, *time.Time) error {
	return nil
}
func (r *stubRunRepoForBinding) FinalizeStatusCAS(context.Context, string, string, string, *time.Time) (bool, error) {
	return true, nil
}
func (r *stubRunRepoForBinding) MarkRunning(context.Context, string, string, time.Time) error {
	return nil
}
func (r *stubRunRepoForBinding) Delete(context.Context, string, string) error { return nil }
func (r *stubRunRepoForBinding) GetLatestSuccessful(context.Context, string, string) (*run.Run, error) {
	return nil, nil
}
func (r *stubRunRepoForBinding) ListTerminalBefore(context.Context, string, time.Time) ([]*run.Run, error) {
	return nil, nil
}
func (r *stubRunRepoForBinding) ExistingIDs(context.Context, []string) (map[string]bool, error) {
	return nil, nil
}

// orderCheckingRPC records, at the moment the dispatch RPC is actually
// sent, whether runRepo already shows the run bound to the agent it's about
// to be sent to — the exact ordering guarantee confirmRunBinding exists for.
type orderCheckingRPC struct {
	repo            *stubRunRepoForBinding
	checked         bool
	boundBeforeSend bool
}

func (r *orderCheckingRPC) SendRPC(ctx context.Context, agentID, method string, payload any, _ any) error {
	if method == iagent.MethodPipelineDispatch {
		task := payload.(*proto.Task)
		existing, _ := r.repo.Get(ctx, task.ProjectID, task.RunID)
		r.checked = true
		r.boundBeforeSend = existing != nil && existing.WorkerID == agentID
	}
	return nil
}

func TestAgentBackendDispatch_ConfirmsRunBindingBeforeSendingRPC(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	repo := newStubRunRepoForBinding()
	rpc := &orderCheckingRPC{repo: repo}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, repo)

	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{ID: "run-1:train", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON}
	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if !rpc.checked {
		t.Fatal("dispatch RPC was never sent")
	}
	if !rpc.boundBeforeSend {
		t.Fatal("runs.worker_id was not yet confirmed at the moment the dispatch RPC was sent")
	}
	if repo.setCalls != 1 {
		t.Fatalf("SetWorkerID called %d times, want exactly 1", repo.setCalls)
	}

	// A second task in the same run reuses the existing binding — no second
	// SetWorkerID call.
	task2 := &proto.Task{ID: "run-1:eval", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON}
	if err := backend.Dispatch(context.Background(), task2); err != nil {
		t.Fatalf("Dispatch (second task) returned error: %v", err)
	}
	if repo.setCalls != 1 {
		t.Fatalf("SetWorkerID called %d times after a second task in the same run, want still 1", repo.setCalls)
	}
}

func TestAgentBackendDispatch_FailsWhenRunAlreadyBoundToDifferentWorker(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	repo := newStubRunRepoForBinding()
	// Simulate the run already being durably bound to a different worker —
	// e.g. surviving state from before a master restart that wiped
	// AgentBackend's in-memory runAgents map.
	repo.bound[runBindingKey("proj-1", "run-1")] = "some-other-worker"
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, repo)

	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	task := &proto.Task{ID: "run-1:train", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON}
	err := backend.Dispatch(context.Background(), task)
	if err == nil {
		t.Fatal("Dispatch succeeded for a run already bound to a different worker")
	}
	var de *DispatchError
	if !errors.As(err, &de) {
		t.Fatalf("error = %#v, want a *DispatchError", err)
	}
	if de.Retryable {
		t.Fatal("a binding conflict must not be retryable — retrying changes nothing")
	}
	if calls := rpc.snapshot(); len(calls) != 0 {
		t.Fatalf("expected no dispatch RPC to be sent, got %#v", calls)
	}
	// The failed dispatch must not leave a dangling runAgents entry behind.
	backend.runMu.Lock()
	_, stillBound := backend.runAgents["run-1"]
	backend.runMu.Unlock()
	if stillBound {
		t.Fatal("runAgents entry was not cleaned up after a binding conflict")
	}
}

// TestAgentBackendDispatch_ConcurrentRootStepsWaitForBindingBeforeSendingRPC
// reproduces two root steps of the same run being dispatched concurrently
// (a fan-out DAG's independent first steps, dispatched from separate
// goroutines — see queue.go's dispatchIfNeeded). The second Dispatch call
// finds the run already bound (the first call already created the
// runAgent) and, without the bindingDone barrier, would skip
// confirmRunBinding entirely and send its own dispatch RPC while the first
// call's DB write is still in flight. Both must wait for that one DB write
// to finish before either sends anything to the worker.
func TestAgentBackendDispatch_ConcurrentRootStepsWaitForBindingBeforeSendingRPC(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       4,
	})
	repo := newStubRunRepoForBinding()
	repo.started = make(chan struct{}, 1)
	repo.gate = make(chan struct{})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, repo)

	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{})
	done := make(chan error, 2)
	go func() {
		done <- backend.Dispatch(context.Background(), &proto.Task{
			ID: "run-1:a", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON,
		})
	}()

	// Wait until the first Dispatch call has actually entered
	// confirmRunBinding (and is now blocked on repo.gate) before launching
	// the second — this deterministically reproduces "a second root step
	// arrives while the first's binding call is still in flight," rather
	// than hoping a sleep wins the race.
	select {
	case <-repo.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first dispatch never reached confirmRunBinding")
	}

	go func() {
		done <- backend.Dispatch(context.Background(), &proto.Task{
			ID: "run-1:b", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON,
		})
	}()

	// Give the second dispatch a moment to reach the point where, without
	// the bindingDone barrier, it would have skipped straight to SendRPC.
	time.Sleep(50 * time.Millisecond)
	if calls := rpc.snapshot(); len(calls) != 0 {
		t.Fatalf("dispatch RPC sent while binding was still unconfirmed: %#v", calls)
	}

	close(repo.gate)

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Dispatch returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Dispatch did not return after the binding gate was opened")
		}
	}

	repo.mu.Lock()
	setCalls := repo.setCalls
	repo.mu.Unlock()
	if setCalls != 1 {
		t.Fatalf("SetWorkerID called %d times for two concurrent root steps of the same run, want exactly 1", setCalls)
	}
	if calls := rpc.snapshot(); len(calls) != 2 {
		t.Fatalf("dispatch RPC calls = %d, want 2 (both must eventually proceed once binding succeeds)", len(calls))
	}
}

// TestAgentBackendDispatch_WaiterReleasesCapacityOnContextCancel reproduces
// a capacity leak: a waiting Dispatch call (a concurrent root step of a run
// whose binding is still in flight — see the bindingDone barrier) already
// holds a router reservation from its own ReserveAgent call by the time it
// starts waiting. If its context is canceled while waiting, that
// reservation must be released — otherwise the worker's capacity slot is
// gone forever even though nothing is actually running against it.
func TestAgentBackendDispatch_WaiterReleasesCapacityOnContextCancel(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
		Capacity:       2, // exactly enough for A (still binding) + B (waiter) — no slack to hide a leak
	})
	router := iagent.NewRouter(reg)
	repo := newStubRunRepoForBinding()
	repo.started = make(chan struct{}, 1)
	repo.gate = make(chan struct{}) // never closed: A's binding call blocks for the whole test
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(router, rpc, repo)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{Spec: pipeline.PipelineSpec{
		Defaults: &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "worker-1"}},
		},
	}})

	go func() {
		_ = backend.Dispatch(context.Background(), &proto.Task{
			ID: "run-1:a", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON,
		})
	}()
	select {
	case <-repo.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first dispatch never reached confirmRunBinding")
	}

	waiterCtx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- backend.Dispatch(waiterCtx, &proto.Task{
			ID: "run-1:b", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON,
		})
	}()
	// Give the waiter time to reach the bindingDone select (it must have
	// already reserved capacity via ReserveAgent by then).
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-waiterDone:
		if err == nil {
			t.Fatal("Dispatch (waiter) succeeded despite its context being canceled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch (waiter) did not return after its context was canceled")
	}

	// worker-1's capacity is exactly 2: A (still holding its reservation,
	// blocked on the gate forever) uses one. If the waiter's reservation
	// leaked, a third, unrelated run must be refused as busy; if it was
	// released correctly, there's exactly one free slot for it. Dispatched
	// through a second AgentBackend sharing the same router (capacity is
	// tracked there, not per-backend) but with its own, ungated repo — this
	// run has nothing to do with A/B's gate and must not be blocked by it.
	otherBackend := NewAgentBackend(router, rpc, newStubRunRepoForBinding())
	otherPipelineJSON, _ := json.Marshal(pipeline.Pipeline{Spec: pipeline.PipelineSpec{
		Defaults: &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "worker-1"}},
		},
	}})
	err := otherBackend.Dispatch(context.Background(), &proto.Task{
		ID: "run-2:only", ProjectID: "proj-1", RunID: "run-2", Pipeline: otherPipelineJSON,
	})
	if err != nil {
		t.Fatalf("dispatch for an unrelated run was refused, capacity leaked: %v", err)
	}
}

// TestAgentBackendDispatch_CancelDuringSendRPCTriggersCompensatingCancel
// reproduces a cancel arriving after Dispatch's pre-send tombstone check has
// already passed but before the dispatch RPC itself returns — a window
// SendRPC (a network call) is genuinely open for. Before the post-send
// re-check, CancelRun would only tombstone in this window (Committed was
// still false when it ran) and never actually tell the worker to stop,
// while Dispatch — unaware anything happened — would mark itself committed
// and return success. The workload would then keep running, canceled only
// in the sense that a caller once asked for it.
func TestAgentBackendDispatch_CancelDuringSendRPCTriggersCompensatingCancel(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "worker-1",
		Infrastructure: iagent.InfrastructureBaremetal,
		Capabilities:   []string{iagent.CapabilityPipeline},
	})
	rpc := &gatedDispatchRPC{
		entered: make(chan struct{}, 1),
		gate:    make(chan struct{}),
	}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	pipelineJSON, _ := json.Marshal(pipeline.Pipeline{Spec: pipeline.PipelineSpec{
		Defaults: &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "worker-1"}},
		},
	}})

	dispatchDone := make(chan error, 1)
	go func() {
		dispatchDone <- backend.Dispatch(context.Background(), &proto.Task{
			ID: "run-1:train", ProjectID: "proj-1", RunID: "run-1", Pipeline: pipelineJSON,
		})
	}()

	// Wait until the dispatch RPC call has actually started (past the
	// pre-send tombstone check) before canceling.
	select {
	case <-rpc.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch RPC was never sent")
	}

	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	// Only now let the in-flight dispatch RPC "complete" at the worker.
	close(rpc.gate)

	select {
	case err := <-dispatchDone:
		if err == nil {
			t.Fatal("Dispatch succeeded for a run canceled while its dispatch RPC was in flight")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch did not return after the RPC gate was opened")
	}

	// A real compensating cancel must have been sent after the dispatch —
	// this is what actually stops the workload, since CancelRun itself only
	// tombstoned (Committed was still false when it ran).
	calls := rpc.snapshot()
	if len(calls) != 2 {
		t.Fatalf("rpc calls = %#v, want exactly 2 (dispatch, then a compensating cancel)", calls)
	}
	if calls[0].Method != iagent.MethodPipelineDispatch {
		t.Fatalf("calls[0].Method = %q, want %q", calls[0].Method, iagent.MethodPipelineDispatch)
	}
	if calls[1].Method != iagent.MethodPipelineCancelRun {
		t.Fatalf("calls[1].Method = %q, want %q (the compensating cancel)", calls[1].Method, iagent.MethodPipelineCancelRun)
	}
}

// gatedDispatchRPC is an AgentRPC whose dispatch call blocks on gate after
// signaling entered, so a test can deterministically land a concurrent
// CancelRun call inside SendRPC's network-call window. Non-dispatch methods
// (e.g. the compensating cancel) return immediately.
type gatedDispatchRPC struct {
	mu      sync.Mutex
	calls   []pipelineAgentRPCCall
	entered chan struct{}
	gate    chan struct{}
}

func (r *gatedDispatchRPC) SendRPC(_ context.Context, agentID, method string, payload any, _ any) error {
	r.mu.Lock()
	r.calls = append(r.calls, pipelineAgentRPCCall{AgentID: agentID, Method: method, Payload: payload})
	r.mu.Unlock()
	if method == iagent.MethodPipelineDispatch {
		r.entered <- struct{}{}
		<-r.gate
	}
	return nil
}

func (r *gatedDispatchRPC) snapshot() []pipelineAgentRPCCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pipelineAgentRPCCall(nil), r.calls...)
}
