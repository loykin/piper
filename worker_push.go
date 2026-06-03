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
	"github.com/piper/piper/pkg/serving"
)

func newWorkerPushHandler(nbMgr *notebook.Manager, servingMgr *serving.Manager) func(ctx context.Context, method string, payload []byte) {
	return func(ctx context.Context, method string, payload []byte) {
		pushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		switch method {
		case iagent.MethodNotebookStatusUpdate:
			if err := handleNotebookStatusPush(pushCtx, payload, nbMgr); err != nil {
				slog.Warn("notebook status push failed", "err", err)
			}
		case iagent.MethodServingStatusUpdate:
			if err := handleServingStatusPush(pushCtx, payload, servingMgr); err != nil {
				slog.Warn("serving status push failed", "err", err)
			}
		default:
			slog.Warn("unknown worker push method", "method", method)
		}
	}
}

type notebookStatusPush struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Endpoint string `json:"endpoint"`
	WorkDir  string `json:"work_dir"`
	Token    string `json:"token"`
	PID      int    `json:"pid"`
	Env      string `json:"env"`
}

func handleNotebookStatusPush(ctx context.Context, payload []byte, nbMgr *notebook.Manager) error {
	var body notebookStatusPush
	if err := json.Unmarshal(payload, &body); err != nil {
		slog.Warn("notebook status push unmarshal failed", "err", err)
		return err
	}
	if body.Name == "" {
		slog.Warn("notebook status push missing name")
		return nil
	}
	return nbMgr.UpdateStatus(ctx, body.Name, body.Status, body.Endpoint, body.WorkDir, body.Token, body.PID, body.Env)
}

type servingStatusPush struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Endpoint string `json:"endpoint"`
}

func handleServingStatusPush(ctx context.Context, payload []byte, servingMgr *serving.Manager) error {
	var body servingStatusPush
	if err := json.Unmarshal(payload, &body); err != nil {
		slog.Warn("serving status push unmarshal failed", "err", err)
		return err
	}
	if body.Name == "" {
		slog.Warn("serving status push missing name")
		return nil
	}
	for attempt := 0; attempt < 20; attempt++ {
		if err := servingMgr.UpdateStatus(ctx, body.Name, body.Status, body.Endpoint); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		return nil
	}
	return sql.ErrNoRows
}
