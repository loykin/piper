package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/piper/piper/pkg/proto"
)

// poll은 master에서 다음 task를 가져온다.
// task가 없으면 (nil, nil)을 반환한다.
func poll(ctx context.Context, client *http.Client, cfg Config, workerID string) (*proto.Task, error) {
	url := fmt.Sprintf("%s/api/tasks/next?worker_id=%s&label=%s", cfg.MasterURL, workerID, cfg.Label)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poll: unexpected status %d", resp.StatusCode)
	}

	var task proto.Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("poll: decode: %w", err)
	}
	return &task, nil
}
