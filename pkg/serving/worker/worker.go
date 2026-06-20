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
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/internal/process"
	"github.com/piper/piper/pkg/serving"
	servingdriver "github.com/piper/piper/pkg/serving/worker/driver"
	servingdocker "github.com/piper/piper/pkg/serving/worker/driver/docker"
	servingprocess "github.com/piper/piper/pkg/serving/worker/driver/process"
)

// Config holds configuration for a serving worker agent.
type Config struct {
	MasterURL   string // single HTTP(S) endpoint for the outbound master tunnel
	WorkerToken string // bearer token sent in gRPC authorization metadata
	GPUs        []string
	Hostname    string
	ID          string // UUID; caller must generate
	Labels      map[string]string
	Mode        string // process | docker; empty means process
	Docker      DockerConfig
}

// serviceRecord holds the worker-observed state of an active service.
// A service is present only while its process is running; it is removed on exit.
type serviceRecord struct {
	status   string
	endpoint string
	gen      uint64 // incremented on each deploy; health goroutines check this to avoid writing stale state
	exitAs   string
}

// Worker manages bare-metal serving workloads and reports their state.
type Worker struct {
	cfg      Config
	client   *grpcagent.Client
	driver   servingdriver.Driver
	mu       sync.Mutex
	services map[string]*serviceRecord // only active (not-yet-exited) services
	nextGen  uint64
}

// New creates a new Worker.
func New(cfg Config) *Worker {
	drv, err := newDriver(cfg)
	if err != nil {
		drv = &failingDriver{err: err}
	}
	mode := cfg.Mode
	if mode == "" {
		mode = servingdriver.ModeProcess
	}
	client := grpcagent.NewClient(grpcagent.ClientConfig{
		MasterURL:    cfg.MasterURL,
		AgentID:      cfg.ID,
		WorkerToken:  cfg.WorkerToken,
		Kind:         iagent.KindBareMetal,
		Mode:         mode,
		Hostname:     cfg.Hostname,
		GPUs:         cfg.GPUs,
		Capabilities: []string{iagent.CapabilityServing},
		Labels:       cfg.Labels,
	})

	w := &Worker{
		cfg:      cfg,
		client:   client,
		driver:   drv,
		services: make(map[string]*serviceRecord),
	}

	d := client.Dispatcher()
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingDeploy, w.deploy)
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingStop, func(ctx context.Context, req servingStopRequest) (any, error) {
		if req.ProjectID == "" {
			return nil, fmt.Errorf("project_id is required")
		}
		return nil, w.stop(ctx, serviceKey(req.ProjectID, req.Name), serviceName(req.ProjectID, req.Name))
	})
	_ = grpcagent.RegisterJSON(d, iagent.MethodServingSyncStatus, w.syncStatus)

	return w
}

func newDriver(cfg Config) (servingdriver.Driver, error) {
	switch cfg.Mode {
	case "", servingdriver.ModeProcess:
		return servingprocess.New(servingprocess.Config{WorkerID: cfg.ID}), nil
	case servingdriver.ModeDocker:
		dockerCfg := cfg.Docker
		dockerCfg.WorkerID = cfg.ID
		return servingdocker.New(dockerCfg)
	default:
		return nil, fmt.Errorf("unsupported serving worker mode %q", cfg.Mode)
	}
}

type failingDriver struct{ err error }

func (d *failingDriver) Deploy(_ context.Context, req servingdriver.DeployRequest) (string, error) {
	if req.LogSink != nil {
		req.LogSink.Stop()
	}
	return "", d.err
}
func (d *failingDriver) Stop(context.Context, string) error { return d.err }
func (d *failingDriver) Status(context.Context, string) string {
	return serving.StatusStopped
}
func (d *failingDriver) KillAll(context.Context) error { return d.err }

// Run connects to the master via gRPC and serves until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.recoverServices(ctx)
	err := w.client.Run(ctx)
	w.shutdown()
	return err
}

type servingStopRequest struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
}

type deployRequest struct {
	ProjectID string `json:"project_id"`
	YAML      string `json:"yaml"`
	LocalPath string `json:"local_path"`
	S3URI     string `json:"s3_uri"`
}

// serviceKey returns the composite map key "projectID:name".
func serviceKey(projectID, name string) string { return projectID + ":" + name }

// serviceName returns the runtime-safe name using "__" separator.
func serviceName(projectID, name string) string {
	if projectID == "" {
		return name
	}
	return projectID + "__" + name
}

type deployResponse struct {
	Endpoint string `json:"endpoint"`
}

func (w *Worker) deploy(_ context.Context, req deployRequest) (*deployResponse, error) {
	if req.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	var svc serving.ModelService
	if err := yaml.Unmarshal([]byte(req.YAML), &svc); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	rt := svc.Spec.Run
	name := svc.Metadata.Name
	if len(rt.Command) == 0 {
		return nil, fmt.Errorf("run.command must not be empty")
	}
	if rt.Port == 0 {
		return nil, fmt.Errorf("run.port must be set")
	}

	key := serviceKey(req.ProjectID, name)
	rn := serviceName(req.ProjectID, name) // runtime-safe composite name

	modelDir := req.LocalPath
	if modelDir == "" {
		modelDir = req.S3URI
	}

	var gpus string
	if svc.Spec.Driver.Process != nil {
		gpus = svc.Spec.Driver.Process.GPUs
	}

	var image string
	if svc.Spec.Driver.Docker != nil {
		image = svc.Spec.Driver.Docker.Image
	}

	endpoint := fmt.Sprintf("http://localhost:%d", rt.Port)
	gen, err := w.reserveService(key, endpoint)
	if err != nil {
		return nil, err
	}

	sink := logsink.NewGRPCLogSink(req.ProjectID, w.client)
	endpoint, err = w.driver.Deploy(context.Background(), servingdriver.DeployRequest{
		ProjectID:   req.ProjectID,
		Name:        name,
		RuntimeName: rn,
		Image:       image,
		Command:     rt.Command,
		Env:         map[string]string{"PIPER_MODEL_DIR": modelDir, "PIPER_SERVICE_NAME": name},
		Port:        rt.Port,
		HealthPath:  rt.HealthPath,
		GPUs:        gpus,
		LogSink:     sink,
		OnExit: func(status string) {
			slog.Info("serving runtime exited", "name", name, "status", status)
			if removed, finalStatus := w.completeService(key, gen, status); removed {
				w.pushStatus(req.ProjectID, name, finalStatus, "")
			}
		},
	})
	if err != nil {
		w.removeService(key, gen)
		return nil, err
	}

	if !w.isCurrentService(key, gen) {
		return &deployResponse{Endpoint: endpoint}, nil
	}

	healthPath := rt.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	go func() {
		if err := process.WaitReady(context.Background(), endpoint+healthPath, 30*time.Second); err != nil {
			slog.Warn("serving health check timed out", "name", name, "endpoint", endpoint)
			// Keep the service tracked while stopping. The exit override makes the
			// OnExit callback report "failed" instead of the stop signal result.
			if w.failService(key, gen) {
				if stopErr := w.driver.Stop(context.Background(), rn); stopErr != nil {
					slog.Warn("serving runtime stop after health failure failed", "name", name, "err", stopErr)
					w.pushStatus(req.ProjectID, name, serving.StatusFailed, "")
				}
			}
			return
		}
		if w.updateService(key, gen, serving.StatusRunning, endpoint) {
			w.pushStatus(req.ProjectID, name, serving.StatusRunning, endpoint)
		}
	}()

	if w.isCurrentService(key, gen) {
		w.pushStatus(req.ProjectID, name, serving.StatusStarting, endpoint)
	}
	return &deployResponse{Endpoint: endpoint}, nil
}

func (w *Worker) reserveService(name, endpoint string) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.services[name]; exists {
		return 0, fmt.Errorf("service %q is already active", name)
	}
	w.nextGen++
	gen := w.nextGen
	w.services[name] = &serviceRecord{status: serving.StatusStarting, endpoint: endpoint, gen: gen}
	return gen, nil
}

func (w *Worker) isCurrentService(name string, gen uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec := w.services[name]
	return rec != nil && rec.gen == gen
}

func (w *Worker) updateService(name string, gen uint64, status, endpoint string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec := w.services[name]
	if rec == nil || rec.gen != gen {
		return false
	}
	rec.status = status
	rec.endpoint = endpoint
	return true
}

func (w *Worker) removeService(name string, gen uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec := w.services[name]
	if rec == nil || rec.gen != gen {
		return false
	}
	delete(w.services, name)
	return true
}

func (w *Worker) failService(name string, gen uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec := w.services[name]
	if rec == nil || rec.gen != gen {
		return false
	}
	rec.status = serving.StatusFailed
	rec.endpoint = ""
	rec.exitAs = serving.StatusFailed
	return true
}

func (w *Worker) completeService(name string, gen uint64, status string) (bool, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec := w.services[name]
	if rec == nil || rec.gen != gen {
		return false, ""
	}
	if rec.exitAs != "" {
		status = rec.exitAs
	}
	delete(w.services, name)
	return true, status
}

func (w *Worker) stop(_ context.Context, key, rn string) error {
	_ = key // key is for services map; rn is the runtime process name
	return w.driver.Stop(context.Background(), rn)
}

func (w *Worker) shutdown() {
	if err := w.driver.KillAll(context.Background()); err != nil {
		slog.Warn("serving worker cleanup failed", "err", err)
	}
}

func (w *Worker) pushStatus(projectID, name, status, endpoint string) {
	if err := w.client.SendPush(iagent.MethodServingStatusUpdate, serving.WorkerStatusUpdate{
		ProjectID: projectID,
		Name:      name,
		Status:    status,
		Endpoint:  endpoint,
	}); err != nil {
		slog.Warn("serving worker: status push failed", "name", name, "status", status, "err", err)
	}
}

// recoverServices delegates native discovery to the selected driver and
// restores the worker state machine for running workloads.
func (w *Worker) recoverServices(ctx context.Context) {
	recoverable, ok := w.driver.(servingdriver.Recoverable)
	if !ok {
		return
	}
	onRecovered := func(handle servingdriver.RecoveredHandle) func(string) {
		key := serviceKey(handle.ProjectID, handle.Name)
		endpoint := fmt.Sprintf("http://localhost:%d", handle.Port)
		w.mu.Lock()
		w.nextGen++
		gen := w.nextGen
		w.services[key] = &serviceRecord{status: serving.StatusRunning, endpoint: endpoint, gen: gen}
		w.mu.Unlock()
		slog.Info("serving worker: recovered service", "name", handle.Name, "port", handle.Port)
		w.pushStatus(handle.ProjectID, handle.Name, serving.StatusRunning, endpoint)
		return func(status string) {
			if removed, finalStatus := w.completeService(key, gen, status); removed {
				w.pushStatus(handle.ProjectID, handle.Name, finalStatus, "")
			}
		}
	}
	onTerminal := func(handle servingdriver.RecoveredHandle, status string) {
		key := serviceKey(handle.ProjectID, handle.Name)
		w.mu.Lock()
		_, active := w.services[key]
		w.mu.Unlock()
		if !active {
			w.pushStatus(handle.ProjectID, handle.Name, status, "")
		}
	}
	if err := recoverable.Recover(ctx, onRecovered, onTerminal); err != nil {
		slog.Warn("serving worker: driver recovery failed", "err", err)
	}
}

// syncStatus answers a master sync request using the worker's own services map
// as the single source of truth. The driver is the execution engine, not the
// state store.
func (w *Worker) syncStatus(_ context.Context, req serving.WorkerSyncStatusRequest) (serving.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Services))
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, target := range req.Services {
		key := serviceKey(target.ProjectID, target.Name)
		if rec, ok := w.services[key]; ok {
			statuses[key] = rec.status
		} else {
			statuses[key] = serving.StatusStopped
		}
	}
	return serving.WorkerSyncStatusResponse{Statuses: statuses}, nil
}
