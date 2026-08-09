package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pendingRequest is a durably queued worker-initiated RPC call.
type pendingRequest struct {
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

// RequestOutbox persists worker-initiated RPC requests (pipeline.step_upsert,
// pipeline.run_finalize) until delivery succeeds. It exists because
// grpcagent.Client.SendRequest — unlike SendPush, which ResultOutbox already
// durably wraps — is a blocking request-response call that fails immediately
// on disconnect rather than queuing: a step_upsert/run_finalize call made
// while the tunnel is down would otherwise be silently lost instead of
// retried once reconnected.
//
// Unlike ResultOutbox, there is no separate ack step: SendRequest's own
// successful return already means the master's CAS handler durably applied
// (or validly rejected, e.g. a stale attempt) the request, so a persisted
// entry is removed the moment delivery succeeds.
type RequestOutbox struct {
	dir  string
	send func(ctx context.Context, method string, payload json.RawMessage) error
	mu   sync.Mutex
}

func NewRequestOutbox(dir string, send func(ctx context.Context, method string, payload json.RawMessage) error) (*RequestOutbox, error) {
	if dir == "" {
		return nil, fmt.Errorf("request outbox directory is required")
	}
	if send == nil {
		return nil, fmt.Errorf("request outbox sender is required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create request outbox: %w", err)
	}
	return &RequestOutbox{dir: dir, send: send}, nil
}

// Enqueue atomically persists a request before attempting delivery. id must
// uniquely identify this specific state transition (e.g.
// "runID:stepName:attempt:status" for a step_upsert call, "runID" for a
// run_finalize call) — a later Enqueue with the same id overwrites the
// pending entry rather than accumulating duplicates, since only the latest
// state for that id is ever worth delivering.
func (o *RequestOutbox) Enqueue(id, method string, payload any) error {
	if id == "" {
		return fmt.Errorf("request outbox entry ID is required")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(pendingRequest{ID: id, Method: method, Payload: raw})
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return writeAtomic(o.path(id), data)
}

// Run repeatedly attempts delivery of every persisted request until each
// succeeds, removing it from disk as soon as it does.
func (o *RequestOutbox) Run(ctx context.Context) {
	o.flush(ctx)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.flush(ctx)
		}
	}
}

func (o *RequestOutbox) flush(ctx context.Context) {
	o.mu.Lock()
	entries, err := os.ReadDir(o.dir)
	o.mu.Unlock()
	if err != nil {
		slog.Warn("request outbox read failed", "err", err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(o.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var req pendingRequest
		if err := json.Unmarshal(data, &req); err != nil {
			slog.Warn("request outbox entry invalid", "file", entry.Name(), "err", err)
			continue
		}
		if err := o.send(ctx, req.Method, req.Payload); err != nil {
			slog.Debug("request outbox delivery deferred", "id", req.ID, "method", req.Method, "err", err)
			continue
		}
		o.mu.Lock()
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("request outbox entry delivered but could not be removed", "id", req.ID, "err", rmErr)
		}
		o.mu.Unlock()
	}
}

func (o *RequestOutbox) path(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(o.dir, hex.EncodeToString(sum[:])+".json")
}
