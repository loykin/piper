package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	worker "github.com/piper/piper/pkg/pipeline/worker"
)

type persistedWorkerMeta struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func loadOrCreateWorkerID(stateDir, role string) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", fmt.Errorf("worker state directory is required")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return "", fmt.Errorf("create worker state directory: %w", err)
	}
	path := filepath.Join(stateDir, role+".json")
	if data, err := os.ReadFile(path); err == nil {
		var meta persistedWorkerMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			return "", fmt.Errorf("decode worker metadata: %w", err)
		}
		if meta.ID != "" && meta.Role == role {
			return meta.ID, nil
		}
		return "", fmt.Errorf("worker metadata is incomplete or belongs to another role")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read worker identity: %w", err)
	}
	id := worker.NewID(role)
	data, err := json.MarshalIndent(persistedWorkerMeta{ID: id, Role: role, CreatedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode worker metadata: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return "", fmt.Errorf("persist worker identity: %w", err)
	}
	return id, nil
}
