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
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/piper/piper/pkg/pipeline/worker/driver"
	baremetaldriver "github.com/piper/piper/pkg/pipeline/worker/driver/baremetal"
	dockerdriver "github.com/piper/piper/pkg/pipeline/worker/driver/docker"
)

// RuntimeType selects how pipeline steps are executed.
type RuntimeType string

const (
	RuntimeBaremetal RuntimeType = "baremetal"
	RuntimeDocker    RuntimeType = "docker"
)

// AgentConfig configures the gRPC connection to the master agent server
// and this worker's identity within the agent registry.
type AgentConfig struct {
	MasterURL   string // single HTTP(S) endpoint for the outbound master tunnel
	WorkerToken string // bearer token for gRPC authorization metadata
	ID          string // stable worker identity
	Label       string
	Labels      map[string]string
	Hostname    string
	Concurrency int
}

// StoreConfig holds the master connection and artifact store settings
// forwarded to every piper agent exec subprocess.
type StoreConfig struct {
	StorageToken string
	StorageURL   string
	OutputDir    string
	RemoteStore  bool // true when using a remote store (S3, HTTP); false for local file://
	// Git source credentials forwarded as PIPER_GIT_USER / PIPER_GIT_TOKEN.
	// Falls back to environment variables when empty.
	GitUser  string
	GitToken string
}

// BaremetalConfig holds options specific to the baremetal subprocess driver.
type BaremetalConfig struct {
	MetaDir string // directory for metadata + PID sidecar files; default: $TMPDIR/piper-meta
}

// DockerConfig holds options specific to the Docker container driver.
type DockerConfig struct {
	Network string
}

// Config holds full Worker configuration grouped by layer.
type Config struct {
	Agent     AgentConfig
	Store     StoreConfig
	Runtime   RuntimeType // baremetal (default) or docker
	Baremetal BaremetalConfig
	Docker    DockerConfig
}

// trackedTask holds state for an in-flight step execution.
type trackedTask struct {
	handle pdriver.Handle
	cancel context.CancelFunc
	logs   logsink.LogSink
}

// Worker manages pipeline workloads via gRPC.
type Worker struct {
	cfg    Config
	client *grpcagent.Client
	driver pdriver.Driver
	outbox *pdriver.ResultOutbox

	mu       sync.Mutex
	active   map[string]*trackedTask // runtimeKey → trackedTask
	inFlight int
}

// New creates a new Worker.
func New(cfg Config) (*Worker, error) {
	if cfg.Agent.Concurrency <= 0 {
		cfg.Agent.Concurrency = 4
	}
	if cfg.Store.OutputDir == "" {
		cfg.Store.OutputDir = "./piper-outputs"
	}
	if cfg.Agent.ID == "" {
		cfg.Agent.ID = NewID("")
	}
	hostname := cfg.Agent.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	labels := make(map[string]string, len(cfg.Agent.Labels)+1)
	for k, v := range cfg.Agent.Labels {
		labels[k] = v
	}
	if cfg.Agent.Label != "" {
		labels["label"] = cfg.Agent.Label
	}

	runtime := string(cfg.Runtime)
	if runtime == "" {
		runtime = string(RuntimeBaremetal)
	}
	client := grpcagent.NewClient(grpcagent.ClientConfig{
		MasterURL:    cfg.Agent.MasterURL,
		AgentID:      cfg.Agent.ID,
		WorkerToken:  cfg.Agent.WorkerToken,
		Kind:         iagent.KindBareMetal,
		Hostname:     hostname,
		Capabilities: []string{iagent.CapabilityPipeline},
		Labels:       labels,
		Runtime:      runtime,
		Capacity:     cfg.Agent.Concurrency,
	})

	var driver pdriver.Driver
	switch cfg.Runtime {
	case RuntimeDocker:
		d, err := dockerdriver.New(dockerdriver.Config{
			WorkerID:  cfg.Agent.ID,
			ResultDir: filepath.Join(cfg.Store.OutputDir, ".results"),
			OutputDir: cfg.Store.OutputDir,
			Network:   cfg.Docker.Network,
		})
		if err != nil {
			return nil, fmt.Errorf("docker driver: %w", err)
		}
		driver = d
	default: // RuntimeBaremetal
		d, err := baremetaldriver.New(baremetaldriver.Config{
			WorkerID:    cfg.Agent.ID,
			MetaDir:     cfg.Baremetal.MetaDir,
			RemoteStore: cfg.Store.RemoteStore,
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
	outbox, err := pdriver.NewResultOutbox(
		filepath.Join(cfg.Store.OutputDir, ".result-outbox", cfg.Agent.ID),
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
	if err := grpcagent.RegisterJSON(d, iagent.MethodPipelineResultAck, func(_ context.Context, ack pdriver.ResultAck) (any, error) {
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
	if obs, ok := w.driver.(pdriver.Observable); ok {
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
	if task.ProjectID == "" {
		return fmt.Errorf("pipeline worker: project_id is required")
	}
	w.mu.Lock()
	if w.inFlight >= w.cfg.Agent.Concurrency {
		w.mu.Unlock()
		return &iagent.BusyError{Reason: "worker at capacity"}
	}
	w.mu.Unlock()

	runtimeKey := pdriver.RuntimeKey(w.cfg.Agent.ID, task.RunID, task.StepName, task.Attempt)

	spec := pdriver.ExecSpec{
		RuntimeKey:   runtimeKey,
		OutputDir:    w.cfg.Store.OutputDir,
		StorageToken: w.cfg.Store.StorageToken,
		StorageURL:   w.cfg.Store.StorageURL,
		Env:          w.gitEnv(),
		LogSink:      logsink.NewGRPCLogSink(task.ProjectID, w.client),
	}

	// Image must be resolved here (in the worker layer) for container runtimes.
	// Baremetal subprocesses run the host binary directly — no image needed.
	if w.cfg.Runtime == RuntimeDocker {
		image, err := pdriver.ResolveImage(task, string(RuntimeDocker))
		if err != nil {
			return err
		}
		spec.Image = image
	}

	handle, err := w.driver.Start(ctx, task, spec)
	if err != nil {
		spec.LogSink.Stop()
		return fmt.Errorf("start job: %w", err)
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.active[runtimeKey] = &trackedTask{handle: handle, cancel: cancel, logs: spec.LogSink}
	w.inFlight++
	w.mu.Unlock()

	go w.observe(taskCtx, handle)
	slog.Info("pipeline step dispatched", "task_id", task.ID, "runtime_key", runtimeKey)
	return nil
}

// observe waits for a job to finish and pushes the result to the master.
func (w *Worker) observe(ctx context.Context, handle pdriver.Handle) {
	defer func() {
		w.mu.Lock()
		tracked := w.active[handle.RuntimeKey]
		delete(w.active, handle.RuntimeKey)
		w.inFlight--
		w.mu.Unlock()
		if tracked != nil && tracked.logs != nil {
			tracked.logs.Stop()
		}
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

func (w *Worker) buildResult(handle pdriver.Handle, exit pdriver.Exit) proto.TaskResult {
	// Driver pre-parsed the result (e.g. K8s reads termination log via K8s API).
	if exit.Result != nil {
		r := *exit.Result
		r.WorkerID = w.cfg.Agent.ID
		return r
	}

	if exit.InfraFailure != nil {
		return proto.TaskResult{
			TaskID:    handle.TaskID,
			WorkerID:  w.cfg.Agent.ID,
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
			if r, err := agent.ReadAgentResult(data); err == nil {
				r.WorkerID = w.cfg.Agent.ID
				return r
			}
		}
	}

	return proto.TaskResult{
		TaskID:   handle.TaskID,
		WorkerID: w.cfg.Agent.ID,
		Status:   proto.TaskStatusFailed,
		Error:    "result unavailable after job completion",
		EndedAt:  time.Now(),
		Attempt:  handle.Attempt,
	}
}

// cancelRun stops all active jobs for the given run.
func (w *Worker) cancelRun(runID string) {
	w.mu.Lock()
	var toStop []trackedTask
	for _, tt := range w.active {
		if tt.handle.RunID == runID {
			toStop = append(toStop, *tt)
			tt.cancel()
		}
	}
	w.mu.Unlock()

	// Stop the drivers using the handles captured under the lock.
	// Re-querying w.active would race with the observe goroutine's cleanup.
	for _, tt := range toStop {
		_ = w.driver.Stop(context.Background(), tt.handle, 10*time.Second)
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
	handles := make([]pdriver.Handle, 0, len(w.active))
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

// gitEnv returns the PIPER_GIT_* environment variables for forwarding to
// piper agent exec subprocesses. Config values take precedence over env vars.
func (w *Worker) gitEnv() []string {
	user := w.cfg.Store.GitUser
	if user == "" {
		user = os.Getenv("PIPER_GIT_USER")
	}
	token := w.cfg.Store.GitToken
	if token == "" {
		token = os.Getenv("PIPER_GIT_TOKEN")
	}
	return []string{
		"PIPER_GIT_USER=" + user,
		"PIPER_GIT_TOKEN=" + token,
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
