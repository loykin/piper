package pipelinedispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/proto"
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
func (r *stubRunRepoForBinding) TouchWorkerLastSeen(context.Context, string, []string) error {
	return nil
}
func (r *stubRunRepoForBinding) SetCancelRequested(context.Context, string, string) (bool, error) {
	return true, nil
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

func TestRunDispatchPlacementRejectsMultipleRunnerLabels(t *testing.T) {
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: t\nspec:\n  steps:\n" +
		"  - name: cpu\n    driver:\n      placement:\n        label: cpu\n    run:\n      command: [\"true\"]\n" +
		"  - name: gpu\n    driver:\n      placement:\n        label: gpu\n    run:\n      command: [\"true\"]\n"

	_, err := runDispatchPlacement(&proto.RunDispatch{RunID: "run-1", PipelineYAML: yaml})
	if err == nil {
		t.Fatal("expected incompatible runner labels to be rejected")
	}
}

func TestRunDispatchPlacementNotebookStepRequiresNotebookCapability(t *testing.T) {
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: t\nspec:\n  steps:\n" +
		"  - name: train\n    run:\n      type: notebook\n"

	placement, err := runDispatchPlacement(&proto.RunDispatch{RunID: "run-1", PipelineYAML: yaml})
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.RequiredCapabilities) != 1 || placement.RequiredCapabilities[0] != iagent.CapabilityNotebook {
		t.Fatalf("required capabilities = %v, want notebook", placement.RequiredCapabilities)
	}
}

// TestRunDispatchPlacementPrefersDispatchWorkerIDOverManifest verifies that
// dispatch.WorkerID — set only when Piper.resendUndeliveredRunDispatches
// resends a run already bound in the DB (see RunDispatch's doc comment) —
// takes priority as an explicit placement, so a resent dispatch goes back to
// the same worker instead of running router selection from scratch.
func TestRunDispatchPlacementPrefersDispatchWorkerIDOverManifest(t *testing.T) {
	yaml := minimalRunDispatchYAML // no defaults.driver.placement.worker at all

	placement, err := runDispatchPlacement(&proto.RunDispatch{RunID: "run-1", PipelineYAML: yaml, WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	if placement.WorkerID != "worker-a" {
		t.Fatalf("placement.WorkerID = %q, want %q", placement.WorkerID, "worker-a")
	}
}

// TestRunDispatchPlacementWorkerIDWinsEvenWithManifestPlacement verifies
// dispatch.WorkerID wins even when the manifest *also* declares an explicit
// placement.worker — dispatch.WorkerID is trusted as the already-resolved
// decision, not re-derived from the manifest.
func TestRunDispatchPlacementWorkerIDWinsEvenWithManifestPlacement(t *testing.T) {
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: t\nspec:\n  defaults:\n    driver:\n      placement:\n        worker: worker-manifest\n  steps:\n  - name: a\n    run:\n      command: [\"true\"]\n"

	placement, err := runDispatchPlacement(&proto.RunDispatch{RunID: "run-1", PipelineYAML: yaml, WorkerID: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	if placement.WorkerID != "worker-a" {
		t.Fatalf("placement.WorkerID = %q, want %q", placement.WorkerID, "worker-a")
	}
}

func TestAgentBackendCancelUsesDispatchAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Infrastructure: iagent.InfrastructureK8s, Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: minimalRunDispatchYAML}
	if err := backend.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun returned error: %v", err)
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	calls := rpc.snapshot()
	if len(calls) != 2 || calls[1].Method != iagent.MethodPipelineCancelRun {
		t.Fatalf("calls = %#v, want dispatch then %q", calls, iagent.MethodPipelineCancelRun)
	}
}

func TestAgentBackendCancelCarriesPipelineNamespace(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Infrastructure: iagent.InfrastructureK8s, Capabilities: []string{iagent.CapabilityPipeline}})
	rpc := &recordingPipelineAgentRPC{}
	backend := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: t\nspec:\n  defaults:\n    driver:\n      k8s:\n        namespace: runs\n  steps:\n  - name: a\n    run:\n      command: [\"true\"]\n"

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: yaml}
	if err := backend.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun returned error: %v", err)
	}
	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	payload := rpc.snapshot()[1].Payload.(map[string]any)
	if payload["namespace"] != "runs" {
		t.Fatalf("namespace = %q, want runs", payload["namespace"])
	}
}

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
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: t\nspec:\n  defaults:\n    driver:\n      placement:\n        worker: agent-1\n  steps:\n  - name: a\n    run:\n      command: [\"true\"]\n"

	reg.Remove("agent-1")
	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: yaml}
	if err := backend.DispatchRun(context.Background(), dispatch); err == nil {
		t.Fatal("expected dispatch to fail when the named worker is gone, not redirect to agent-2")
	}
	if len(rpc.snapshot()) != 0 {
		t.Fatalf("dispatch calls = %d, want 0 (must not have silently used agent-2)", len(rpc.snapshot()))
	}
}

func TestAgentBackendDispatchRun_AppliesPodPolicyToDefaults(t *testing.T) {
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
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: t\nspec:\n  defaults:\n    driver:\n      k8s:\n        image: train:latest\n  steps:\n  - name: a\n    run:\n      command: [\"true\"]\n"

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: yaml}
	if err := b.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun error: %v", err)
	}
	calls := rpc.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 RPC call, got %d", len(calls))
	}
	sent, ok := calls[0].Payload.(*proto.RunDispatch)
	if !ok {
		t.Fatalf("payload type: %T", calls[0].Payload)
	}
	sentPl, err := pipeline.Parse([]byte(sent.PipelineYAML))
	if err != nil {
		t.Fatalf("parse sent pipeline: %v", err)
	}
	if sentPl.Spec.Defaults == nil || sentPl.Spec.Defaults.Driver.K8s == nil {
		t.Fatal("defaults K8s driver should be present in sent pipeline")
	}
	ns := sentPl.Spec.Defaults.Driver.K8s.PodTemplate.Spec.NodeSelector
	if ns["tier"] != "gpu" {
		t.Errorf("policy nodeSelector not applied: got %v", ns)
	}
}

func TestAgentBackendDispatchRun_ManifestWinsOverPolicy(t *testing.T) {
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
	yaml := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: t\nspec:\n  defaults:\n    driver:\n" +
		"      k8s:\n        image: train:latest\n        pod_template:\n          spec:\n            nodeSelector:\n              tier: manifest\n" +
		"  steps:\n  - name: a\n    run:\n      command: [\"true\"]\n"

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: yaml}
	if err := b.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun error: %v", err)
	}
	sent := rpc.snapshot()[0].Payload.(*proto.RunDispatch)
	sentPl, err := pipeline.Parse([]byte(sent.PipelineYAML))
	if err != nil {
		t.Fatalf("parse sent pipeline: %v", err)
	}
	ns := sentPl.Spec.Defaults.Driver.K8s.PodTemplate.Spec.NodeSelector
	if ns["tier"] != "manifest" {
		t.Errorf("manifest should win over policy: tier=%q (want manifest)", ns["tier"])
	}
}

func TestAgentBackendDispatchRun_NoPolicyNoChange(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "k8s-1",
		Capabilities: []string{iagent.CapabilityPipeline},
		Capacity:     1,
	})
	rpc := &recordingPipelineAgentRPC{}
	// no policy repo passed → NewAgentBackend with no variadic arg
	b := NewAgentBackend(iagent.NewRouter(reg), rpc, nil)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: minimalRunDispatchYAML}
	if err := b.DispatchRun(context.Background(), dispatch); err != nil {
		t.Fatalf("DispatchRun error: %v", err)
	}
	sent := rpc.snapshot()[0].Payload.(*proto.RunDispatch)
	// pipeline YAML should be identical — no merge happened
	if sent.PipelineYAML != minimalRunDispatchYAML {
		t.Errorf("pipeline should be unchanged when no policy repo is configured:\ngot:  %q\nwant: %q", sent.PipelineYAML, minimalRunDispatchYAML)
	}
}
