package servingdispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/serving"
)

// WorkerDriver implements Driver by delegating to a registered ServingWorker agent.
type WorkerDriver struct {
	registry  *serving.ServingWorkerRegistry
	repo      serving.Repository
	masterURL string // base URL of the master, for worker callbacks
}

// NewWorkerDriver creates a WorkerDriver backed by the given registry.
func NewWorkerDriver(registry *serving.ServingWorkerRegistry, repo serving.Repository, masterURL string) *WorkerDriver {
	return &WorkerDriver{registry: registry, repo: repo, masterURL: masterURL}
}

func (d *WorkerDriver) ArtifactTarget() artifact.Target { return artifact.TargetLocal }

// Deploy picks an available worker and sends it a deploy request.
func (d *WorkerDriver) Deploy(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) (*serving.Service, error) {
	var w *serving.ServingWorkerInfo
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
		"local_path": art.LocalPath,
		"s3_uri":     art.S3URI,
		"master_url": d.masterURL,
	})
	if err != nil {
		return nil, fmt.Errorf("worker deploy: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Addr+"/deploy", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("worker deploy: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker deploy: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("worker deploy: worker %s returned status %d: %s", w.Addr, resp.StatusCode, bytes.TrimSpace(body))
	}

	name := spec.Metadata.Name
	artifactLabel := ""
	if spec.Spec.Model.FromArtifact != nil {
		artifactLabel = spec.Spec.Model.FromArtifact.Step + "/" + spec.Spec.Model.FromArtifact.Artifact
	} else if spec.Spec.Model.FromURI != "" {
		artifactLabel = spec.Spec.Model.FromURI
	}

	svc := &serving.Service{
		Name:     name,
		Artifact: artifactLabel,
		Status:   serving.StatusRunning,
		WorkerID: w.ID,
		YAML:     yamlStr,
	}
	return svc, nil
}

// workerFor returns the worker that owns svc, falling back to Pick() if worker_id is unset or expired.
func (d *WorkerDriver) workerFor(svc *serving.Service) (*serving.ServingWorkerInfo, error) {
	if svc != nil && svc.WorkerID != "" {
		if w, err := d.registry.Get(svc.WorkerID); err == nil {
			return w, nil
		}
	}
	return d.registry.Pick()
}

// Stop asks the worker that owns the service to stop it.
func (d *WorkerDriver) Stop(ctx context.Context, svc *serving.Service) error {
	w, err := d.workerFor(svc)
	if err != nil {
		// Worker gone; treat as already stopped.
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/service/%s", w.Addr, svc.Name), nil)
	if err != nil {
		return fmt.Errorf("worker stop: build request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("worker stop: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("worker stop: worker %s returned status %d", w.Addr, resp.StatusCode)
	}
	return nil
}

// Restart asks the worker that owns the service to restart it.
func (d *WorkerDriver) Restart(ctx context.Context, spec serving.ModelService, _ artifact.Resolved, _ string) error {
	existing, _ := d.repo.Get(ctx, spec.Metadata.Name)
	// workerFor handles nil gracefully via Pick() fallback.
	w, err := d.workerFor(existing)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/service/%s/restart", w.Addr, spec.Metadata.Name), nil)
	if err != nil {
		return fmt.Errorf("worker restart: build request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("worker restart: call worker %s: %w", w.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("worker restart: worker %s returned status %d", w.Addr, resp.StatusCode)
	}
	return nil
}
