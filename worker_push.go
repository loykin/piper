package piper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/notebook"
	pdriver "github.com/piper/piper/pkg/pipeline/worker/driver"
	"github.com/piper/piper/pkg/serving"
)

type pipelineStatusQueue interface {
	Complete(ctx context.Context, result proto.TaskResult) error
	RenewLeases(workerID string, taskIDs []string)
}

type pipelineResultAcker interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

func newWorkerPushHandler(nbMgr *notebook.Manager, servingMgr *serving.Manager, pipelineQueue pipelineStatusQueue, acker pipelineResultAcker, logs logstore.LogStore, metrics logstore.MetricStore) func(ctx context.Context, agentID, method string, payload []byte) {
	return func(ctx context.Context, agentID, method string, payload []byte) {
		pushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		switch method {
		case iagent.MethodLogAppend:
			if logs == nil {
				return
			}
			var req logsink.LogAppendPush
			if err := json.Unmarshal(payload, &req); err != nil {
				slog.Warn("log append push unmarshal failed", "agent_id", agentID, "err", err)
				return
			}
			lines := make([]*logstore.Line, 0, len(req.Lines))
			metricRows := make([]*logstore.Metric, 0)
			for _, l := range req.Lines {
				lines = append(lines, &logstore.Line{
					ProjectID: req.ProjectID,
					RunID:     req.RunID,
					StepName:  req.StepName,
					Ts:        l.Ts,
					Stream:    l.Stream,
					Line:      l.Text,
				})
				if key, value, ok := parsePushedMetric(l.Text); ok && metrics != nil {
					metricRows = append(metricRows, &logstore.Metric{ProjectID: req.ProjectID, RunID: req.RunID, StepName: req.StepName, Key: key, Value: value, Ts: l.Ts})
				}
			}
			if err := logs.Append(pushCtx, lines); err != nil {
				slog.Warn("log append push write failed", "agent_id", agentID, "run_id", req.RunID, "err", err)
			}
			if metrics != nil && len(metricRows) > 0 {
				if err := metrics.AppendMetrics(pushCtx, metricRows); err != nil {
					slog.Warn("metric append push failed", "agent_id", agentID, "run_id", req.RunID, "err", err)
				}
			}
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
			if metrics != nil && len(result.Metrics) > 0 {
				runID, stepName, ok := strings.Cut(result.TaskID, ":")
				if ok {
					now := time.Now().UTC()
					rows := make([]*logstore.Metric, 0, len(result.Metrics))
					for key, value := range result.Metrics {
						rows = append(rows, &logstore.Metric{ProjectID: result.ProjectID, RunID: runID, StepName: stepName, Key: key, Value: value, Ts: now})
					}
					if err := metrics.AppendMetrics(pushCtx, rows); err != nil {
						slog.Warn("pipeline metrics push failed", "agent_id", agentID, "task_id", result.TaskID, "err", err)
					}
				}
			}
			if err := pipelineQueue.Complete(pushCtx, result); err != nil {
				slog.Warn("pipeline result push failed", "agent_id", agentID, "task_id", result.TaskID, "err", err)
				return
			}
			if acker != nil {
				ack := pdriver.ResultAck{TaskID: result.TaskID, Attempt: result.Attempt}
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

func parsePushedMetric(line string) (string, float64, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "PIPER_METRIC ") {
		return "", 0, false
	}
	key, raw, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "PIPER_METRIC ")), "=")
	if !ok {
		return "", 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	key = strings.TrimSpace(key)
	return key, value, key != "" && err == nil
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
	return nbMgr.UpdateStatus(ctx, body.ProjectID, agentID, body.Name, body.Status, body.Endpoint, body.WorkDir, body.Token, body.PID, body.Env)
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
	if err := servingMgr.UpdateStatus(ctx, body.ProjectID, agentID, body.Name, body.Status, body.Endpoint); err != nil {
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
