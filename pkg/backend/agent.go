package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

type AgentRPC interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

type AgentBackend struct {
	router     *iagent.Router
	rpc        AgentRPC
	runAgents  sync.Map // run id -> pipelineRunAgent
	taskAgents sync.Map // task id -> agent id
}

type pipelineRunAgent struct {
	AgentID   string
	Namespace string
}

func NewAgentBackend(router *iagent.Router, rpc AgentRPC) *AgentBackend {
	return &AgentBackend{router: router, rpc: rpc}
}

func (b *AgentBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	if b == nil || b.router == nil || b.rpc == nil {
		return fmt.Errorf("pipeline agent backend is not configured")
	}
	placement, err := taskPlacement(task)
	if err != nil {
		return err
	}
	agentInfo, err := b.router.Select(iagent.WorkloadPipeline, placement)
	if err != nil {
		return err
	}
	taskCopy := *task
	taskCopy.WorkerID = agentInfo.ID
	b.taskAgents.Store(task.ID, agentInfo.ID)
	if err := b.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodPipelineDispatch, &taskCopy, nil); err != nil {
		b.taskAgents.Delete(task.ID)
		return fmt.Errorf("pipeline agent dispatch: %w", err)
	}
	b.runAgents.Store(task.RunID, pipelineRunAgent{AgentID: agentInfo.ID, Namespace: placement.Namespace})
	return nil
}

func (b *AgentBackend) OwnerForTask(taskID string) string {
	owner, ok := b.taskAgents.Load(taskID)
	if !ok {
		return ""
	}
	return owner.(string)
}

func (b *AgentBackend) ReleaseTask(taskID string) {
	b.taskAgents.Delete(taskID)
}

func (b *AgentBackend) CancelRun(ctx context.Context, runID string) error {
	if b == nil || b.rpc == nil {
		return fmt.Errorf("pipeline agent backend is not configured")
	}
	agentIDAny, ok := b.runAgents.Load(runID)
	if !ok {
		return nil
	}
	runAgent := agentIDAny.(pipelineRunAgent)
	if err := b.rpc.SendRPC(ctx, runAgent.AgentID, iagent.MethodPipelineCancelRun, map[string]any{
		"run_id":    runID,
		"namespace": runAgent.Namespace,
	}, nil); err != nil {
		return fmt.Errorf("pipeline agent cancel: %w", err)
	}
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
	placement := iagent.Placement{
		WorkerID:    pl.Spec.Placement.Worker,
		ClusterName: pl.Spec.Placement.Cluster,
		Namespace:   pl.Spec.Placement.Namespace,
		Labels:      pl.Spec.Placement.Labels,
	}
	if placement.WorkerID == "" && placement.ClusterName == "" && len(placement.Labels) == 0 && task.Label != "" {
		placement.Labels = map[string]string{"label": task.Label}
	}
	return placement, nil
}

var _ ExecutionBackend = (*AgentBackend)(nil)
var _ CancelableBackend = (*AgentBackend)(nil)
var _ TaskOwner = (*AgentBackend)(nil)
