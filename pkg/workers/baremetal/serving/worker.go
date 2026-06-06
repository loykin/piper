// Package servingworker implements the serving worker agent.
// Run one of these on each bare-metal node that should execute serving processes.
// It connects to the master via gRPC and handles serving lifecycle commands.
package servingworker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/workload"
)

// Config holds configuration for a serving worker agent.
type Config struct {
	AgentAddr string // gRPC address of master agent server, e.g. "master:9090"
	GPUs      []string
	Hostname  string
	ID        string // UUID; caller must generate
}

// serviceRecord holds the worker-observed state of an active service.
// A service is present only while its process is running; it is removed on exit.
type serviceRecord struct {
	status   string
	endpoint string
}

// Worker is the serving worker agent.
type Worker struct {
	cfg        Config
	client     *grpcagent.Client
	supervisor *workload.ProcessSupervisor
	mu         sync.Mutex
	services   map[string]*serviceRecord // only active (not-yet-exited) services
}

// New creates a new Worker.
func New(cfg Config) *Worker {
	client := grpcagent.NewClient(grpcagent.ClientConfig{
		AgentAddr:    cfg.AgentAddr,
		AgentID:      cfg.ID,
		Kind:         iagent.KindBareMetal,
		Hostname:     cfg.Hostname,
		GPUs:         cfg.GPUs,
		Capabilities: []string{iagent.CapabilityServing},
	})

	w := &Worker{
		cfg:        cfg,
		client:     client,
		supervisor: workload.NewProcessSupervisor(),
		services:   make(map[string]*serviceRecord),
	}

	d := client.Dispatcher()
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingDeploy, w.deploy)
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingStop, func(ctx context.Context, req servingStopRequest) (any, error) {
		return nil, w.stop(ctx, req.Name)
	})
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingSyncStatus, w.syncStatus)

	return w
}

// Run connects to the master via gRPC and serves until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	err := w.client.Run(ctx)
	w.shutdown()
	return err
}

type servingStopRequest struct {
	Name string `json:"name"`
}

type deployRequest struct {
	YAML      string `json:"yaml"`
	LocalPath string `json:"local_path"`
	S3URI     string `json:"s3_uri"`
}

type deployResponse struct {
	Endpoint string `json:"endpoint"`
}

func (w *Worker) deploy(_ context.Context, req deployRequest) (*deployResponse, error) {
	var svc serving.ModelService
	if err := yaml.Unmarshal([]byte(req.YAML), &svc); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	rt := svc.Spec.Runtime
	name := svc.Metadata.Name
	if len(rt.Command) == 0 {
		return nil, fmt.Errorf("runtime.command must not be empty")
	}
	if rt.Port == 0 {
		return nil, fmt.Errorf("runtime.port must be set")
	}

	modelDir := req.LocalPath
	if modelDir == "" {
		modelDir = req.S3URI
	}

	spec := workload.ProcessSpec{
		Name:       name,
		Command:    rt.Command,
		Env:        map[string]string{"PIPER_MODEL_DIR": modelDir, "PIPER_SERVICE_NAME": name},
		Port:       rt.Port,
		HealthPath: rt.HealthPath,
		GPUs:       rt.GPUs,
	}

	_, endpoint, err := w.supervisor.Start(spec, func(status string) {
		// Process exited — remove from services map, then push final status.
		slog.Info("serving process exited", "name", name, "status", status)
		w.mu.Lock()
		delete(w.services, name)
		w.mu.Unlock()
		w.pushStatus(name, status, "")
	})
	if err != nil {
		return nil, err
	}

	// Register the service as "starting"; health check goroutine will update this.
	w.mu.Lock()
	w.services[name] = &serviceRecord{status: serving.StatusStarting, endpoint: endpoint}
	w.mu.Unlock()

	healthPath := rt.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	go func() {
		if err := workload.WaitReady(context.Background(), endpoint+healthPath, 30*time.Second); err != nil {
			slog.Warn("serving health check timed out", "name", name, "endpoint", endpoint)
			w.mu.Lock()
			if rec, ok := w.services[name]; ok {
				rec.status = serving.StatusFailed
				rec.endpoint = ""
			}
			w.mu.Unlock()
			w.pushStatus(name, serving.StatusFailed, "")
			return
		}
		w.mu.Lock()
		if rec, ok := w.services[name]; ok {
			rec.status = serving.StatusRunning
		}
		w.mu.Unlock()
		w.pushStatus(name, serving.StatusRunning, endpoint)
	}()

	w.pushStatus(name, serving.StatusStarting, endpoint)
	return &deployResponse{Endpoint: endpoint}, nil
}

func (w *Worker) stop(_ context.Context, name string) error {
	return w.supervisor.Stop(name)
}

func (w *Worker) shutdown() {
	_ = w.supervisor.KillAll()
}

func (w *Worker) pushStatus(name, status, endpoint string) {
	if err := w.client.SendPush(iagent.MethodServingStatusUpdate, serving.WorkerStatusUpdate{
		Name:     name,
		Status:   status,
		Endpoint: endpoint,
	}); err != nil {
		slog.Warn("serving worker: status push failed", "name", name, "status", status, "err", err)
	}
}

// syncStatus answers a master sync request using the worker's own services map
// as the single source of truth. supervisor is not queried here — it is the
// process engine, not the state store.
func (w *Worker) syncStatus(_ context.Context, req serving.WorkerSyncStatusRequest) (serving.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Services))
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, target := range req.Services {
		if rec, ok := w.services[target.Name]; ok {
			statuses[target.Name] = rec.status
		} else {
			statuses[target.Name] = serving.StatusStopped
		}
	}
	return serving.WorkerSyncStatusResponse{Statuses: statuses}, nil
}
