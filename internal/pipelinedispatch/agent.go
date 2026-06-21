package pipelinedispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
)

type AgentRPC interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

type AgentBackend struct {
	router      *iagent.Router
	rpc         AgentRPC
	podPolicies iagent.WorkerPodPolicyRepository
	taskAgents  sync.Map // task id -> pipelineTaskAgent

	runMu     sync.Mutex
	runAgents map[string]*pipelineRunAgent // run id -> fixed agent for the whole run
}

type pipelineRunAgent struct {
	AgentID   string
	Namespace string
	Committed bool
	Pending   int
}

type pipelineTaskAgent struct {
	AgentID string
}

func NewAgentBackend(router *iagent.Router, rpc AgentRPC, policies ...iagent.WorkerPodPolicyRepository) *AgentBackend {
	b := &AgentBackend{
		router:    router,
		rpc:       rpc,
		runAgents: make(map[string]*pipelineRunAgent),
	}
	if len(policies) > 0 {
		b.podPolicies = policies[0]
	}
	return b
}

func (b *AgentBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	if b == nil || b.router == nil || b.rpc == nil {
		return fmt.Errorf("pipeline agent backend is not configured")
	}
	placement, err := taskPlacement(task)
	if err != nil {
		return err
	}
	b.runMu.Lock()
	runAgent, bound := b.runAgents[task.RunID]
	if !bound {
		agentInfo, selectErr := b.router.Reserve(iagent.WorkloadPipeline, placement)
		if selectErr != nil {
			b.runMu.Unlock()
			return selectErr
		}
		runAgent = &pipelineRunAgent{
			AgentID:   agentInfo.ID,
			Namespace: placement.Namespace,
		}
		b.runAgents[task.RunID] = runAgent
	} else if reserveErr := b.router.ReserveAgent(runAgent.AgentID, iagent.WorkloadPipeline); reserveErr != nil {
		b.runMu.Unlock()
		return &DispatchError{Retryable: true, Err: reserveErr}
	}
	runAgent.Pending++
	b.runMu.Unlock()

	taskCopy := *task
	taskCopy.WorkerID = runAgent.AgentID
	if b.podPolicies != nil {
		policy, pErr := b.podPolicies.Get(ctx, runAgent.AgentID)
		if pErr != nil {
			slog.Warn("pipeline: pod policy lookup failed, proceeding without policy",
				"worker_id", runAgent.AgentID, "err", pErr)
		} else if policy != nil {
			if merged, mErr := applyPodPolicyToPipeline(taskCopy.Pipeline, policy.PodTemplate); mErr == nil {
				taskCopy.Pipeline = merged
			} else {
				slog.Warn("pipeline: pod policy merge failed, using original pipeline",
					"worker_id", runAgent.AgentID, "err", mErr)
			}
		}
	}
	b.taskAgents.Store(task.ID, pipelineTaskAgent{AgentID: runAgent.AgentID})
	if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineDispatch, &taskCopy, nil); err != nil {
		b.taskAgents.Delete(task.ID)
		b.router.Release(runAgent.AgentID)
		b.runMu.Lock()
		runAgent.Pending--
		if !runAgent.Committed && runAgent.Pending == 0 && b.runAgents[task.RunID] == runAgent {
			delete(b.runAgents, task.RunID)
		}
		b.runMu.Unlock()
		// Worker capacity refusal: BusyErrorMarker is embedded in the error string
		// because gRPC serialises the error to a plain string on the wire.
		if strings.Contains(err.Error(), iagent.BusyErrorMarker) {
			return &DispatchError{Retryable: true, Err: err}
		}
		return fmt.Errorf("pipeline agent dispatch: %w", err)
	}
	b.runMu.Lock()
	runAgent.Pending--
	runAgent.Committed = true
	b.runMu.Unlock()
	return nil
}

func (b *AgentBackend) OwnerForTask(taskID string) string {
	owner, ok := b.taskAgents.Load(taskID)
	if !ok {
		return ""
	}
	return owner.(pipelineTaskAgent).AgentID
}

func (b *AgentBackend) ReleaseTask(taskID string) {
	owner, ok := b.taskAgents.LoadAndDelete(taskID)
	if !ok {
		return
	}
	b.router.Release(owner.(pipelineTaskAgent).AgentID)
}

func (b *AgentBackend) ReleaseRun(runID string) {
	b.runMu.Lock()
	delete(b.runAgents, runID)
	b.runMu.Unlock()
}

func (b *AgentBackend) CancelRun(ctx context.Context, runID string) error {
	if b == nil || b.rpc == nil {
		return fmt.Errorf("pipeline agent backend is not configured")
	}
	b.runMu.Lock()
	runAgent, ok := b.runAgents[runID]
	b.runMu.Unlock()
	if !ok {
		return nil
	}
	if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineCancelRun, map[string]any{
		"run_id":    runID,
		"namespace": runAgent.Namespace,
	}, nil); err != nil {
		return fmt.Errorf("pipeline agent cancel: %w", err)
	}
	b.ReleaseRun(runID)
	return nil
}

func taskPlacement(task *proto.Task) (iagent.Placement, error) {
	if task == nil {
		return iagent.Placement{}, fmt.Errorf("task is required")
	}
	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return iagent.Placement{}, fmt.Errorf("unmarshal pipeline: %w", err)
	}
	var defaults pipeline.PipelineDefaults
	if pl.Spec.Defaults != nil {
		defaults = *pl.Spec.Defaults
	}
	var ns string
	if defaults.Driver.K8s != nil {
		ns = defaults.Driver.K8s.Namespace
	}
	placement := iagent.Placement{
		WorkerID:         defaults.Driver.Placement.Worker,
		Namespace:        ns,
		RequireContainer: pipelineRequiresContainer(&pl),
	}
	if label := defaults.Driver.Placement.Label; label != "" {
		placement.Labels = map[string]string{"label": label}
	}
	if placement.WorkerID == "" && len(placement.Labels) == 0 {
		label, err := pipelineRunnerLabel(&pl)
		if err != nil {
			return iagent.Placement{}, err
		}
		if label != "" {
			placement.Labels = map[string]string{"label": label}
		}
	}
	return placement, nil
}

func pipelineRequiresContainer(pl *pipeline.Pipeline) bool {
	if pl.Spec.Defaults != nil && driverHasContainerImage(pl.Spec.Defaults.Driver) {
		return true
	}
	for _, step := range pl.Spec.Steps {
		if driverHasContainerImage(step.Driver) {
			return true
		}
	}
	return false
}

func driverHasContainerImage(driver manifest.DriverSpec) bool {
	return driver.Docker != nil && driver.Docker.Image != "" ||
		driver.K8s != nil && driver.K8s.Image != ""
}

func pipelineRunnerLabel(pl *pipeline.Pipeline) (string, error) {
	var label string
	for _, step := range pl.Spec.Steps {
		if step.Driver.Placement.Label == "" {
			continue
		}
		if label == "" {
			label = step.Driver.Placement.Label
			continue
		}
		if label != step.Driver.Placement.Label {
			return "", fmt.Errorf(
				"pipeline requires multiple runner labels (%q and %q); a run must execute on one worker",
				label,
				step.Driver.Placement.Label,
			)
		}
	}
	return label, nil
}

var _ ExecutionBackend = (*AgentBackend)(nil)
var _ CancelableBackend = (*AgentBackend)(nil)
var _ TaskOwner = (*AgentBackend)(nil)
var _ RunOwner = (*AgentBackend)(nil)
