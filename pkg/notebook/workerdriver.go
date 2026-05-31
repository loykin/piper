package notebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WorkerDriver implements Driver by delegating to a registered NotebookWorker agent.
type WorkerDriver struct {
	registry  *NotebookWorkerRegistry
	masterURL string // base URL of the master, for worker callbacks
}

// NewWorkerDriver creates a WorkerDriver backed by the given registry.
func NewWorkerDriver(registry *NotebookWorkerRegistry, masterURL string) *WorkerDriver {
	return &WorkerDriver{registry: registry, masterURL: masterURL}
}

// Start picks an available worker and sends it a start request.
func (d *WorkerDriver) Start(ctx context.Context, spec NotebookServerSpec, yamlStr string) (*NotebookServer, error) {
	var w *NotebookWorkerInfo
	var err error
	if spec.Spec.Runtime.Worker != "" {
		w, err = d.registry.GetByHostname(spec.Spec.Runtime.Worker)
	} else {
		w, err = d.registry.Pick()
	}
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"yaml":       yamlStr,
		"master_url": d.masterURL,
	})
	if err != nil {
		return nil, fmt.Errorf("notebook worker start: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Addr+"/start", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("notebook worker start: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("notebook worker start: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("notebook worker start: worker %s returned status %d: %s", w.Addr, resp.StatusCode, bytes.TrimSpace(body))
	}

	var result struct {
		Endpoint string `json:"endpoint"`
		Token    string `json:"token"`
		PID      int    `json:"pid"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	nb := &NotebookServer{
		Name:     spec.Metadata.Name,
		Status:   StatusRunning,
		Endpoint: result.Endpoint,
		Token:    result.Token,
		PID:      result.PID,
		WorkDir:  spec.Spec.Runtime.WorkDir,
		WorkerID: w.ID,
	}
	return nb, nil
}

// workerFor returns the worker that owns nb, falling back to Pick() if worker_id is unset or expired.
func (d *WorkerDriver) workerFor(nb *NotebookServer) (*NotebookWorkerInfo, error) {
	if nb != nil && nb.WorkerID != "" {
		if w, err := d.registry.Get(nb.WorkerID); err == nil {
			return w, nil
		}
	}
	return d.registry.Pick()
}

// Stop asks the worker that owns the notebook to stop it.
func (d *WorkerDriver) Stop(ctx context.Context, nb *NotebookServer) error {
	w, err := d.workerFor(nb)
	if err != nil {
		// Worker gone; treat as already stopped.
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/notebook/%s", w.Addr, nb.Name), nil)
	if err != nil {
		return fmt.Errorf("notebook worker stop: build request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notebook worker stop: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("notebook worker stop: worker %s returned status %d", w.Addr, resp.StatusCode)
	}
	return nil
}
