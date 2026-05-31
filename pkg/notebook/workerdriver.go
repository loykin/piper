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

// ProvisionVolume picks a worker, creates the volume directory on it, and
// populates vol.WorkDir and vol.WorkerID (stored as hostname for readability).
// If vol.WorkerID is already set the request is pinned to that worker (node affinity).
func (d *WorkerDriver) ProvisionVolume(ctx context.Context, vol *NotebookVolume, _ string) error {
	var w *NotebookWorkerInfo
	var err error
	if vol.WorkerID != "" {
		w, err = d.registry.GetByHostname(vol.WorkerID)
	} else {
		w, err = d.registry.Pick()
	}
	if err != nil {
		return err
	}

	payload, _ := json.Marshal(map[string]any{"volume_id": vol.ID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Addr+"/volume", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("provision volume: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("provision volume: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("provision volume: worker %s returned %d: %s", w.Addr, resp.StatusCode, bytes.TrimSpace(body))
	}

	var result struct {
		WorkDir string `json:"work_dir"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	vol.WorkDir = result.WorkDir
	vol.WorkerID = w.Hostname
	return nil
}

// Start picks an available worker and sends it a start request.
// Worker selection priority: vol.WorkerID (node affinity) > spec.Spec.Worker > Pick().
func (d *WorkerDriver) Start(ctx context.Context, spec NotebookServerSpec, vol *NotebookVolume, yamlStr string) (*NotebookServer, error) {
	var w *NotebookWorkerInfo
	var err error
	if vol != nil && vol.WorkerID != "" {
		// Volume has node affinity; must start on the same worker.
		w, err = d.registry.GetByHostname(vol.WorkerID)
		if err != nil {
			return nil, fmt.Errorf("volume's worker %q is unavailable: %w", vol.WorkerID, err)
		}
	} else if spec.Spec.Worker != "" {
		w, err = d.registry.GetByHostname(spec.Spec.Worker)
		if err != nil {
			return nil, err
		}
	} else {
		w, err = d.registry.Pick()
		if err != nil {
			return nil, err
		}
	}

	workDir := ""
	volumeID := ""
	if vol != nil {
		workDir = vol.WorkDir
		volumeID = vol.ID
	}

	payload, err := json.Marshal(map[string]any{
		"yaml":       yamlStr,
		"master_url": d.masterURL,
		"work_dir":   workDir,
		"volume_id":  volumeID,
	})
	if err != nil {
		return nil, fmt.Errorf("notebook worker start: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Addr+"/start", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("notebook worker start: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Worker returns 202 Accepted immediately; actual startup is async.
	// Use a short timeout since the worker should respond immediately now.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("notebook worker start: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle both 200 (sync, legacy) and 202 (async) responses.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("notebook worker start: worker %s returned status %d: %s", w.Addr, resp.StatusCode, bytes.TrimSpace(body))
	}

	var result struct {
		Token   string `json:"token"`
		WorkDir string `json:"work_dir"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	nb := &NotebookServer{
		Name:     spec.Metadata.Name,
		Status:   StatusStarting,
		Token:    result.Token,
		WorkDir:  result.WorkDir,
		WorkerID: w.Hostname,
	}
	return nb, nil
}

// workerFor returns the worker that owns nb, falling back to Pick() if worker_id is unset or offline.
func (d *WorkerDriver) workerFor(nb *NotebookServer) (*NotebookWorkerInfo, error) {
	if nb != nil && nb.WorkerID != "" {
		if w, err := d.registry.GetByHostname(nb.WorkerID); err == nil {
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

// DeprovisionVolume asks the worker that owns the volume to permanently remove its
// work directory. If WorkDir is empty the volume was never provisioned; nothing to do.
func (d *WorkerDriver) DeprovisionVolume(ctx context.Context, vol *NotebookVolume) error {
	if vol.WorkDir == "" {
		return nil // nothing was ever provisioned
	}

	var w *NotebookWorkerInfo
	var err error
	if vol.WorkerID != "" {
		w, err = d.registry.GetByHostname(vol.WorkerID)
	} else {
		w, err = d.registry.Pick()
	}
	if err != nil {
		return nil // worker gone; treat as already cleaned up
	}

	url := fmt.Sprintf("%s/volume/%s?work_dir=%s", w.Addr, vol.ID, vol.WorkDir)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("deprovision volume: build request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("deprovision volume: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("deprovision volume: worker %s returned %d", w.Addr, resp.StatusCode)
	}
	return nil
}
