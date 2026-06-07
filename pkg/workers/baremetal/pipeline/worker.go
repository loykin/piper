// Package pipelineworker implements a bare-metal pipeline worker that connects
// to the master via gRPC and executes steps as isolated subprocesses using
// piper agent exec.
package pipelineworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/taskruntime"
	"github.com/piper/piper/pkg/taskruntime/baremetal"
	dockerdriver "github.com/piper/piper/pkg/taskruntime/docker"
)

// RuntimeType selects how pipeline steps are executed.
type RuntimeType string

const (
	RuntimeBaremetal RuntimeType = "baremetal"
	RuntimeDocker    RuntimeType = "docker"
)

// Config holds Worker configuration.
type Config struct {
	AgentAddr   string // gRPC address of master agent server, e.g. "master:9090"
	ID          string // stable worker identity (UUID)
	Label       string
	Hostname    string
	Concurrency int
	// Runtime selects the execution environment: "baremetal" (default) or "docker".
	Runtime RuntimeType
	// Artifact store configuration passed through to piper agent exec.
	MasterURL  string
	Token      string
	StorageURL string
	OutputDir  string
	// MetaDir is the directory for baremetal runtime metadata sidecar files.
	MetaDir     string
	RemoteStore bool
	// Docker-specific config (only used when Runtime == "docker").
	DefaultImage  string
	DockerNetwork string
}

// trackedTask holds state for an in-flight step execution.
type trackedTask struct {
	handle taskruntime.Handle
	cancel context.CancelFunc
}

// Worker manages pipeline workloads via gRPC.
type Worker struct {
	cfg    Config
	client *grpcagent.Client
	driver taskruntime.Driver
	outbox *taskruntime.ResultOutbox

	mu       sync.Mutex
	active   map[string]*trackedTask // runtimeKey → trackedTask
	inFlight int
}

// New creates a new Worker.
func New(cfg Config) (*Worker, error) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./piper-outputs"
	}
	hostname := cfg.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	labels := map[string]string{}
	if cfg.Label != "" {
		labels["label"] = cfg.Label
	}

	runtime := string(cfg.Runtime)
	if runtime == "" {
		runtime = string(RuntimeBaremetal)
	}
	client := grpcagent.NewClient(grpcagent.ClientConfig{
		AgentAddr:    cfg.AgentAddr,
		AgentID:      cfg.ID,
		Kind:         iagent.KindBareMetal,
		Hostname:     hostname,
		Capabilities: []string{iagent.CapabilityPipeline},
		Labels:       labels,
		Runtime:      runtime,
		Capacity:     cfg.Concurrency,
	})

	var driver taskruntime.Driver
	switch cfg.Runtime {
	case RuntimeDocker:
		d, err := dockerdriver.New(dockerdriver.Config{
			WorkerID:     cfg.ID,
			DefaultImage: cfg.DefaultImage,
			ResultDir:    filepath.Join(cfg.OutputDir, ".results"),
			OutputDir:    cfg.OutputDir,
			Network:      cfg.DockerNetwork,
		})
		if err != nil {
			return nil, fmt.Errorf("docker driver: %w", err)
		}
		driver = d
	default: // RuntimeBaremetal
		d, err := baremetal.New(baremetal.Config{
			WorkerID:    cfg.ID,
			MetaDir:     cfg.MetaDir,
			RemoteStore: cfg.RemoteStore,
		})
		if err != nil {
			return nil, fmt.Errorf("baremetal driver: %w", err)
		}
		driver = d
	}

	w := &Worker{
		cfg:    cfg,
		client: client,
		driver: driver,
		active: make(map[string]*trackedTask),
	}
	closeDriver := func() {
		if closer, ok := driver.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	outbox, err := taskruntime.NewResultOutbox(
		filepath.Join(cfg.OutputDir, ".result-outbox", cfg.ID),
		func(result proto.TaskResult) error {
			return client.SendPush(iagent.MethodPipelineTaskResult, result)
		},
	)
	if err != nil {
		closeDriver()
		return nil, err
	}
	w.outbox = outbox

	d := client.Dispatcher()
	if err := grpcagent.RegisterJSON(d, iagent.MethodPipelineDispatch, func(ctx context.Context, task proto.Task) (any, error) {
		return nil, w.dispatch(ctx, &task)
	}); err != nil {
		closeDriver()
		return nil, err
	}
	if err := grpcagent.RegisterJSON(d, iagent.MethodPipelineCancelRun, func(_ context.Context, req cancelRunRequest) (any, error) {
		w.cancelRun(req.RunID)
		return nil, nil
	}); err != nil {
		closeDriver()
		return nil, err
	}
	if err := grpcagent.RegisterJSON(d, iagent.MethodPipelineResultAck, func(_ context.Context, ack taskruntime.ResultAck) (any, error) {
		return nil, w.outbox.Ack(ack)
	}); err != nil {
		closeDriver()
		return nil, err
	}

	return w, nil
}

type cancelRunRequest struct {
	RunID string `json:"run_id"`
}

// Run connects to the master and serves until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	// Recover any jobs that survived a previous worker restart.
	if handles, err := w.driver.Recover(ctx); err != nil {
		slog.Warn("pipeline worker: recovery failed", "err", err)
	} else {
		for _, h := range handles {
			taskCtx, cancel := context.WithCancel(ctx)
			w.mu.Lock()
			w.active[h.RuntimeKey] = &trackedTask{handle: h, cancel: cancel}
			w.inFlight++
			w.mu.Unlock()
			go w.observe(taskCtx, h)
		}
		if len(handles) > 0 {
			slog.Info("pipeline worker: recovered jobs", "count", len(handles))
		}
	}

	// Drivers that need a background reconcile loop (e.g. K8s) implement Observable.
	if obs, ok := w.driver.(taskruntime.Observable); ok {
		go obs.Observe(ctx)
	}

	go w.leaseLoop(ctx)
	go w.outbox.Run(ctx)

	err := w.client.Run(ctx)
	w.shutdown()
	return err
}

// dispatch is called by the gRPC dispatcher when the master sends a pipeline.dispatch RPC.
func (w *Worker) dispatch(ctx context.Context, task *proto.Task) error {
	w.mu.Lock()
	if w.inFlight >= w.cfg.Concurrency {
		w.mu.Unlock()
		return &iagent.BusyError{Reason: "worker at capacity"}
	}
	w.mu.Unlock()

	runtimeKey := taskruntime.RuntimeKey(w.cfg.ID, task.RunID, task.StepName, task.Attempt)

	// ExecSpec is environment-agnostic. Each Driver resolves paths and builds
	// agent exec args internally based on its own execution environment.
	spec := taskruntime.ExecSpec{
		RuntimeKey:    runtimeKey,
		HostOutputDir: w.cfg.OutputDir,
		MasterURL:     w.cfg.MasterURL,
		Token:         w.cfg.Token,
		StorageURL:    w.cfg.StorageURL,
		Env: []string{
			"PIPER_GIT_USER=" + os.Getenv("PIPER_GIT_USER"),
			"PIPER_GIT_TOKEN=" + os.Getenv("PIPER_GIT_TOKEN"),
		},
	}

	handle, err := w.driver.Start(ctx, task, spec)
	if err != nil {
		return fmt.Errorf("start job: %w", err)
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.active[runtimeKey] = &trackedTask{handle: handle, cancel: cancel}
	w.inFlight++
	w.mu.Unlock()

	go w.observe(taskCtx, handle)
	slog.Info("pipeline step dispatched", "task_id", task.ID, "runtime_key", runtimeKey)
	return nil
}

// observe waits for a job to finish and pushes the result to the master.
func (w *Worker) observe(ctx context.Context, handle taskruntime.Handle) {
	defer func() {
		w.mu.Lock()
		delete(w.active, handle.RuntimeKey)
		w.inFlight--
		w.mu.Unlock()
	}()

	exit, err := w.driver.Wait(ctx, handle)
	if err != nil {
		// ctx cancelled (worker shutdown or run cancel).
		return
	}

	result := w.buildResult(handle, exit)
	if err := w.outbox.Enqueue(result); err != nil {
		slog.Error("pipeline worker: persist result failed", "task_id", result.TaskID, "err", err)
	}
}

func (w *Worker) buildResult(handle taskruntime.Handle, exit taskruntime.Exit) proto.TaskResult {
	// Driver pre-parsed the result (e.g. K8s reads termination log via K8s API).
	if exit.Result != nil {
		r := *exit.Result
		r.WorkerID = w.cfg.ID
		return r
	}

	if exit.InfraFailure != nil {
		return proto.TaskResult{
			TaskID:    handle.TaskID,
			WorkerID:  w.cfg.ID,
			Status:    proto.TaskStatusFailed,
			Error:     exit.InfraFailure.Error(),
			StartedAt: time.Now(),
			EndedAt:   time.Now(),
			Attempt:   handle.Attempt,
		}
	}

	// Read the result file written by piper agent exec (baremetal/docker).
	if exit.ResultPath != "" {
		if data, err := os.ReadFile(exit.ResultPath); err == nil {
			if r, err := taskruntime.ReadAgentResult(data); err == nil {
				r.WorkerID = w.cfg.ID
				return r
			}
		}
	}

	return proto.TaskResult{
		TaskID:   handle.TaskID,
		WorkerID: w.cfg.ID,
		Status:   proto.TaskStatusFailed,
		Error:    "result unavailable after job completion",
		EndedAt:  time.Now(),
		Attempt:  handle.Attempt,
	}
}

// cancelRun stops all active jobs for the given run.
func (w *Worker) cancelRun(runID string) {
	w.mu.Lock()
	var toCancel []string
	for key, tt := range w.active {
		if tt.handle.RunID == runID {
			toCancel = append(toCancel, key)
			tt.cancel()
		}
	}
	w.mu.Unlock()

	for _, key := range toCancel {
		w.mu.Lock()
		tt := w.active[key]
		w.mu.Unlock()
		if tt != nil {
			_ = w.driver.Stop(context.Background(), tt.handle, 10*time.Second)
		}
	}
}

// leaseLoop pushes active task IDs to the master every 10 seconds.
func (w *Worker) leaseLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.mu.Lock()
			taskIDs := make([]string, 0, len(w.active))
			for _, tt := range w.active {
				taskIDs = append(taskIDs, tt.handle.TaskID)
			}
			w.mu.Unlock()
			if len(taskIDs) == 0 {
				continue
			}
			payload := map[string]any{"task_ids": taskIDs}
			data, _ := json.Marshal(payload)
			if err := w.client.SendPush(iagent.MethodPipelineLeaseRenew, json.RawMessage(data)); err != nil {
				slog.Warn("pipeline worker: lease renew failed", "err", err)
			}
		}
	}
}

// shutdown stops all in-flight jobs gracefully.
func (w *Worker) shutdown() {
	w.mu.Lock()
	handles := make([]taskruntime.Handle, 0, len(w.active))
	for _, tt := range w.active {
		tt.cancel()
		handles = append(handles, tt.handle)
	}
	w.mu.Unlock()

	for _, h := range handles {
		_ = w.driver.Stop(context.Background(), h, 15*time.Second)
	}
	if closer, ok := w.driver.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

// sanitizeName normalises a string to be a safe process name (lowercase alnum + dash).
func sanitizeName(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// NewID generates a stable worker ID from prefix and hostname.
// Multiple workers on one host must configure distinct explicit IDs.
func NewID(prefix string) string {
	host, _ := os.Hostname()
	if host == "" {
		host = "worker"
	}
	if prefix != "" {
		host = prefix + "-" + host
	}
	return sanitizeName(host)
}
