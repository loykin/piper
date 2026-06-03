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

// Worker is the serving worker agent.
type Worker struct {
	cfg      Config
	client   *grpcagent.Client
	mu       sync.Mutex
	services map[string]*localService
}

type localService struct {
	name string
	pid  int
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
		cfg:      cfg,
		client:   client,
		services: make(map[string]*localService),
	}

	d := client.Dispatcher()
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingDeploy, w.deploy)
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingStop, func(ctx context.Context, req servingStopRequest) (any, error) {
		return nil, w.stop(ctx, req.Name)
	})
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingRestart, func(ctx context.Context, req servingRestartRequest) (any, error) {
		return nil, w.restart(ctx, req.Name)
	})

	return w
}

// Run connects to the master via gRPC and serves until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	return w.client.Run(ctx)
}

type servingStopRequest struct {
	Name string `json:"name"`
}

type servingRestartRequest struct {
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

	pid, endpoint, cmd, err := workload.StartProcess(spec)
	if err != nil {
		return nil, err
	}

	w.mu.Lock()
	w.services[name] = &localService{name: name, pid: pid}
	w.mu.Unlock()

	workload.WatchProcess(cmd, func(status string) {
		slog.Info("serving process exited", "name", name, "status", status)
		w.mu.Lock()
		delete(w.services, name)
		w.mu.Unlock()
		w.pushStatus(name, status, "")
	})

	healthPath := rt.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	go func() {
		if err := workload.WaitReady(context.Background(), endpoint+healthPath, 30*time.Second); err != nil {
			slog.Warn("serving health check timed out", "name", name, "endpoint", endpoint)
		}
	}()

	w.pushStatus(name, serving.StatusRunning, endpoint)
	return &deployResponse{Endpoint: endpoint}, nil
}

func (w *Worker) stop(_ context.Context, name string) error {
	w.mu.Lock()
	ls, ok := w.services[name]
	if ok {
		delete(w.services, name)
	}
	w.mu.Unlock()

	if ok {
		workload.KillPID(ls.pid)
	}
	return nil
}

func (w *Worker) restart(_ context.Context, name string) error {
	w.mu.Lock()
	ls, ok := w.services[name]
	if ok {
		delete(w.services, name)
	}
	w.mu.Unlock()

	if ok {
		workload.KillPID(ls.pid)
	}
	return nil
}

func (w *Worker) pushStatus(name, status, endpoint string) {
	type payload struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Endpoint string `json:"endpoint,omitempty"`
	}
	if err := w.client.SendPush(iagent.MethodServingStatusUpdate, payload{
		Name:     name,
		Status:   status,
		Endpoint: endpoint,
	}); err != nil {
		slog.Warn("serving worker: status push failed", "name", name, "status", status, "err", err)
	}
}
