package piper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/taskruntime"
)

type pipelineStatusQueue interface {
	Complete(ctx context.Context, result proto.TaskResult) error
	RenewLeases(workerID string, taskIDs []string)
}

type pipelineResultAcker interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

func newWorkerPushHandler(nbMgr *notebook.Manager, servingMgr *serving.Manager, pipelineQueue pipelineStatusQueue, acker pipelineResultAcker) func(ctx context.Context, agentID, method string, payload []byte) {
	return func(ctx context.Context, agentID, method string, payload []byte) {
		pushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		switch method {
		case iagent.MethodNotebookStatusUpdate:
			if err := handleNotebookStatusPush(pushCtx, agentID, payload, nbMgr); err != nil {
				slog.Warn("notebook status push failed", "agent_id", agentID, "err", err)
			}
		case iagent.MethodServingStatusUpdate:
			if err := handleServingStatusPush(pushCtx, agentID, payload, servingMgr); err != nil {
				slog.Warn("serving status push failed", "agent_id", agentID, "err", err)
			}
		case iagent.MethodPipelineLeaseRenew:
			var body struct {
				TaskIDs []string `json:"task_ids"`
			}
			if err := json.Unmarshal(payload, &body); err != nil {
				slog.Warn("pipeline lease push unmarshal failed", "agent_id", agentID, "err", err)
				return
			}
			pipelineQueue.RenewLeases(agentID, body.TaskIDs)
		case iagent.MethodPipelineTaskResult:
			var result proto.TaskResult
			if err := json.Unmarshal(payload, &result); err != nil {
				slog.Warn("pipeline result push unmarshal failed", "agent_id", agentID, "err", err)
				return
			}
			result.WorkerID = agentID
			if err := pipelineQueue.Complete(pushCtx, result); err != nil {
				slog.Warn("pipeline result push failed", "agent_id", agentID, "task_id", result.TaskID, "err", err)
				return
			}
			if acker != nil {
				ack := taskruntime.ResultAck{TaskID: result.TaskID, Attempt: result.Attempt}
				ackCtx, ackCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer ackCancel()
				if err := acker.SendRPC(ackCtx, agentID, iagent.MethodPipelineResultAck, ack, nil); err != nil {
					slog.Warn("pipeline result ack failed", "agent_id", agentID, "task_id", result.TaskID, "err", err)
				}
			}
		default:
			slog.Warn("unknown worker push method", "agent_id", agentID, "method", method)
		}
	}
}

func handleNotebookStatusPush(ctx context.Context, agentID string, payload []byte, nbMgr *notebook.Manager) error {
	var body notebook.WorkerStatusUpdate
	if err := json.Unmarshal(payload, &body); err != nil {
		slog.Warn("notebook status push unmarshal failed", "err", err)
		return err
	}
	if body.Name == "" {
		slog.Warn("notebook status push missing name")
		return nil
	}
	return nbMgr.UpdateStatus(ctx, agentID, body.Name, body.Status, body.Endpoint, body.WorkDir, body.Token, body.PID, body.Env)
}

func handleServingStatusPush(ctx context.Context, agentID string, payload []byte, servingMgr *serving.Manager) error {
	var body serving.WorkerStatusUpdate
	if err := json.Unmarshal(payload, &body); err != nil {
		slog.Warn("serving status push unmarshal failed", "err", err)
		return err
	}
	if body.Name == "" {
		slog.Warn("serving status push missing name")
		return nil
	}
	if err := servingMgr.UpdateStatus(ctx, agentID, body.Name, body.Status, body.Endpoint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Push arrived before deploy RPC completed on master — drop silently.
			// The worker will push the final status (running/failed) after health check.
			slog.Debug("serving status push dropped: service not yet registered", "name", body.Name, "status", body.Status)
			return nil
		}
		return err
	}
	return nil
}
