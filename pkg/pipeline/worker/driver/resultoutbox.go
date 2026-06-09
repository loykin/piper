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

	"github.com/piper/piper/internal/proto"
)

// ResultAck identifies a result durably accepted by the master.
type ResultAck struct {
	TaskID  string `json:"task_id"`
	Attempt int    `json:"attempt"`
}

// ResultOutbox persists task results until the master acknowledges them.
type ResultOutbox struct {
	dir  string
	send func(proto.TaskResult) error
	mu   sync.Mutex
}

func NewResultOutbox(dir string, send func(proto.TaskResult) error) (*ResultOutbox, error) {
	if dir == "" {
		return nil, fmt.Errorf("result outbox directory is required")
	}
	if send == nil {
		return nil, fmt.Errorf("result outbox sender is required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create result outbox: %w", err)
	}
	return &ResultOutbox{dir: dir, send: send}, nil
}

// Enqueue atomically persists a result before attempting delivery.
func (o *ResultOutbox) Enqueue(result proto.TaskResult) error {
	if result.TaskID == "" {
		return fmt.Errorf("result task ID is required")
	}
	if result.Attempt < 1 {
		result.Attempt = 1
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return writeAtomic(o.path(result.TaskID, result.Attempt), data)
}

// Ack removes a result only after the master has durably accepted it.
func (o *ResultOutbox) Ack(ack ResultAck) error {
	if ack.TaskID == "" {
		return fmt.Errorf("result ack task ID is required")
	}
	if ack.Attempt < 1 {
		ack.Attempt = 1
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	err := os.Remove(o.path(ack.TaskID, ack.Attempt))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Run repeatedly delivers every persisted result until it is acknowledged.
func (o *ResultOutbox) Run(ctx context.Context) {
	o.flush()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.flush()
		}
	}
}

func (o *ResultOutbox) flush() {
	o.mu.Lock()
	entries, err := os.ReadDir(o.dir)
	o.mu.Unlock()
	if err != nil {
		slog.Warn("result outbox read failed", "err", err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.dir, entry.Name()))
		if err != nil {
			continue
		}
		var result proto.TaskResult
		if err := json.Unmarshal(data, &result); err != nil {
			slog.Warn("result outbox entry invalid", "file", entry.Name(), "err", err)
			continue
		}
		if err := o.send(result); err != nil {
			slog.Debug("result outbox delivery deferred", "task_id", result.TaskID, "err", err)
		}
	}
}

func (o *ResultOutbox) path(taskID string, attempt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", taskID, attempt)))
	return filepath.Join(o.dir, hex.EncodeToString(sum[:])+".json")
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".result-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
