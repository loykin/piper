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

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/serving"
)

// runLivenessRecorder records a run-level heartbeat from a worker whose
// scheduler (pkg/pipeline/worker/scheduler) owns a run — pushed every 10s
// via pipeline.lease_renew's run_ids. Satisfied by pkg/pipeline/run.Repository.
type runLivenessRecorder interface {
	TouchWorkerLastSeen(ctx context.Context, workerID string, runIDs []string) error
}

func newWorkerPushHandler(nbMgr *notebook.Manager, servingMgr *serving.Manager, runLiveness runLivenessRecorder, logs logstore.LogStore, metrics logstore.MetricStore) func(ctx context.Context, agentID, method string, payload []byte) {
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
				RunIDs []string `json:"run_ids"`
			}
			if err := json.Unmarshal(payload, &body); err != nil {
				slog.Warn("pipeline lease push unmarshal failed", "agent_id", agentID, "err", err)
				return
			}
			if len(body.RunIDs) > 0 && runLiveness != nil {
				if err := runLiveness.TouchWorkerLastSeen(pushCtx, agentID, body.RunIDs); err != nil {
					slog.Warn("pipeline run liveness touch failed", "agent_id", agentID, "err", err)
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
