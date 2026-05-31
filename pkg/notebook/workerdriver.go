package notebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	w, err := d.registry.Pick()
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
		return nil, fmt.Errorf("notebook worker start: worker %s returned status %d", w.Addr, resp.StatusCode)
	}

	name := spec.Metadata.Name
	nb := &NotebookServer{
		Name:    name,
		Status:  StatusRunning,
		WorkDir: spec.Spec.Runtime.WorkDir,
	}
	return nb, nil
}

// Stop asks the worker that owns the notebook to stop it.
func (d *WorkerDriver) Stop(ctx context.Context, nb *NotebookServer) error {
	w, err := d.registry.Pick()
	if err != nil {
		// Worker may be gone; treat as already stopped.
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
