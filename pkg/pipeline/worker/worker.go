// Package pipelineworker implements a bare-metal pipeline worker that connects
// to the master via gRPC and executes steps as isolated subprocesses using
// piper agent exec.
package pipelineworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/grpcagent"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	baremetaldriver "github.com/loykin/piper/pkg/pipeline/worker/driver/baremetal"
	dockerdriver "github.com/loykin/piper/pkg/pipeline/worker/driver/docker"
)

// RuntimeType selects how pipeline steps are executed.
type RuntimeType string

const (
	RuntimeBaremetal RuntimeType = "baremetal"
	RuntimeDocker    RuntimeType = "docker"
)

// defaultShutdownGrace bounds how long a worker waits for in-flight jobs to
// stop gracefully during shutdown when AgentConfig.ShutdownGrace is unset.
const defaultShutdownGrace = 20 * time.Second

// AgentConfig configures the gRPC connection to the master agent server
// and this worker's identity within the agent registry.
type AgentConfig struct {
	MasterURL    string // single HTTP(S) endpoint for the outbound master tunnel
	WorkerToken  string // bearer token for gRPC authorization metadata
	ID           string // stable worker identity
	Label        string
	Labels       map[string]string
	Hostname     string
	Concurrency  int
	Capabilities []string // execution capabilities; pipeline is always included
	// ShutdownGrace bounds how long shutdown() waits for in-flight jobs to
	// stop gracefully before Run returns. Defaults to 20s.
	ShutdownGrace time.Duration
}

// StoreConfig holds the master connection and artifact store settings
// forwarded to every piper agent exec subprocess.
type StoreConfig struct {
	OutputDir        string
	RemoteStore      bool // true when using a remote store (S3, HTTP); false for local file://
	LocalStoreAccess bool // true only for embedded workers sharing the master's filesystem
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

// trackedTask holds state for an in-flight step execution. It is registered
// (with starting=true and no handle yet) atomically with the capacity
// reservation in dispatch, before the potentially-slow driver.Start call, so
// a cancelRun arriving mid-Start can find and cancel it instead of finding
// nothing.
type trackedTask struct {
	runID    string // set at reservation time, before handle exists
	handle   pdriver.Handle
	cancel   context.CancelFunc
	logs     logsink.LogSink
	starting bool // true between reservation and driver.Start returning
	canceled bool // set by cancelRun if it canceled this entry while starting
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
	draining bool
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
	if cfg.Agent.ShutdownGrace <= 0 {
		cfg.Agent.ShutdownGrace = defaultShutdownGrace
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
	infrastructure := iagent.InfrastructureBaremetal
	if runtime == string(RuntimeDocker) {
		infrastructure = iagent.InfrastructureDocker
	}
	capabilities := append([]string(nil), cfg.Agent.Capabilities...)
	if len(capabilities) == 0 {
		capabilities = []string{iagent.CapabilityPipeline}
	} else {
		hasPipeline := false
		for _, capability := range capabilities {
			if capability == iagent.CapabilityPipeline {
				hasPipeline = true
				break
			}
		}
		if !hasPipeline {
			capabilities = append([]string{iagent.CapabilityPipeline}, capabilities...)
		}
	}
	client := grpcagent.NewClient(grpcagent.ClientConfig{
		MasterURL:      cfg.Agent.MasterURL,
		AgentID:        cfg.Agent.ID,
		WorkerToken:    cfg.Agent.WorkerToken,
		Infrastructure: infrastructure,
		Hostname:       hostname,
		Capabilities:   capabilities,
		Labels:         labels,
		Capacity:       cfg.Agent.Concurrency,
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
		return nil, w.cancelRun(req.RunID)
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), w.cfg.Agent.ShutdownGrace)
	defer cancel()
	w.shutdown(shutdownCtx)
	return err
}

// dispatch is called by the gRPC dispatcher when the master sends a pipeline.dispatch RPC.
func (w *Worker) dispatch(_ context.Context, task *proto.Task) error {
	if task.ProjectID == "" {
		return fmt.Errorf("pipeline worker: project_id is required")
	}

	runtimeKey := pdriver.RuntimeKey(w.cfg.Agent.ID, task.RunID, task.StepName, task.Attempt)
	var taskCtx context.Context
	var cancel context.CancelFunc
	if task.Deadline != nil && !task.Deadline.IsZero() {
		taskCtx, cancel = context.WithDeadline(context.Background(), *task.Deadline)
	} else {
		taskCtx, cancel = context.WithCancel(context.Background())
	}

	// Reserve capacity and a "starting" placeholder atomically with the
	// capacity check, before the potentially-slow driver.Start call below.
	// This closes the race where two concurrent dispatches both observe
	// spare capacity and both Start, exceeding configured concurrency, and
	// makes an in-progress Start interruptible by cancelRun (which can now
	// find and cancel a "starting" entry instead of finding nothing).
	w.mu.Lock()
	if w.draining {
		w.mu.Unlock()
		cancel()
		return &iagent.BusyError{Reason: "worker draining"}
	}
	if w.inFlight >= w.cfg.Agent.Concurrency {
		w.mu.Unlock()
		cancel()
		return &iagent.BusyError{Reason: "worker at capacity"}
	}
	w.inFlight++
	w.active[runtimeKey] = &trackedTask{runID: task.RunID, cancel: cancel, starting: true}
	w.mu.Unlock()

	rollback := func() {
		w.mu.Lock()
		delete(w.active, runtimeKey)
		w.inFlight--
		w.mu.Unlock()
	}

	storageURL, storageToken := taskStorageForWorker(task, w.cfg.Agent.MasterURL, w.cfg.Agent.WorkerToken, w.cfg.Store.LocalStoreAccess)
	execEnv := mergeExecutionEnv(w.gitEnv(), task.Env)
	taskCopy := *task
	taskCopy.Env = execEnv

	spec := pdriver.ExecSpec{
		RuntimeKey:   runtimeKey,
		OutputDir:    w.cfg.Store.OutputDir,
		StorageToken: storageToken,
		StorageURL:   storageURL,
		LogSink:      logsink.NewRedactingSink(logsink.NewGRPCLogSink(task.ProjectID, w.client), logsink.ValuesFromEnv(execEnv)),
	}

	// Image must be resolved here (in the worker layer) for container runtimes.
	// Baremetal subprocesses run the host binary directly — no image needed.
	if w.cfg.Runtime == RuntimeDocker {
		image, err := pdriver.ResolveImage(&taskCopy, string(RuntimeDocker))
		if err != nil {
			rollback()
			spec.LogSink.Stop()
			return err
		}
		spec.Image = image
	}

	handle, err := w.driver.Start(taskCtx, &taskCopy, spec)
	if err != nil {
		rollback()
		spec.LogSink.Stop()
		return fmt.Errorf("start job: %w", err)
	}

	w.mu.Lock()
	tt, stillTracked := w.active[runtimeKey]
	canceledMidStart := !stillTracked || tt.canceled
	if canceledMidStart {
		// cancelRun marked this "starting" entry canceled (or, defensively,
		// it's altogether missing) while Start was in flight. Don't publish
		// it as active or hand it to observe — stop what was just started
		// and clean up the reservation instead.
		delete(w.active, runtimeKey)
		w.inFlight--
		w.mu.Unlock()
		_ = w.driver.Stop(context.Background(), handle, 10*time.Second)
		spec.LogSink.Stop()
		return nil
	}
	tt.handle = handle
	tt.logs = spec.LogSink
	tt.starting = false
	w.mu.Unlock()

	go w.observe(taskCtx, handle)
	slog.Info("pipeline step dispatched", "task_id", task.ID, "runtime_key", runtimeKey)
	return nil
}

func mergeEnv(base, override []string) []string {
	if len(base) == 0 {
		return append([]string{}, override...)
	}
	out := append([]string{}, base...)
	index := make(map[string]int, len(out))
	for i, item := range out {
		if eq := strings.IndexByte(item, '='); eq > 0 {
			index[item[:eq]] = i
		}
	}
	for _, item := range override {
		eq := strings.IndexByte(item, '=')
		if eq <= 0 {
			out = append(out, item)
			continue
		}
		key := item[:eq]
		if i, ok := index[key]; ok {
			out[i] = item
		} else {
			index[key] = len(out)
			out = append(out, item)
		}
	}
	return out
}

func mergeExecutionEnv(base, override []string) []string {
	merged := mergeEnv(base, override)
	if pdriver.EnvValue(override, "PIPER_GIT_TOKEN") != "" && pdriver.EnvValue(override, "PIPER_GIT_USER") == "" {
		merged = removeEnvKey(merged, "PIPER_GIT_USER")
	}
	return merged
}

func removeEnvKey(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func taskStorageForWorker(task *proto.Task, masterURL, workerToken string, localStoreAccess bool) (storageURL, storageToken string) {
	if task == nil {
		return "", ""
	}
	storageURL = task.StorageURL
	storageToken = task.StorageToken
	if strings.HasPrefix(storageURL, "file://") && !localStoreAccess {
		storageURL = strings.TrimRight(strings.TrimSpace(masterURL), "/") + "/store"
		if storageToken == "" {
			storageToken = workerToken
		}
	}
	return storageURL, storageToken
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
		// The master's own timeout enforcement (via Queue.startTaskLocked's
		// timer) is authoritative; this is a local backstop so the process
		// actually stops even if the master tunnel is down. An explicit
		// cancelRun()/shutdown() already owns Stop() for the Canceled case —
		// only act here when our own deadline fired.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = w.driver.Stop(context.Background(), handle, 10*time.Second)
			result := proto.TaskResult{
				TaskID:    handle.TaskID,
				WorkerID:  w.cfg.Agent.ID,
				Status:    proto.TaskStatusFailed,
				Error:     "task execution timeout",
				StartedAt: time.Now(),
				EndedAt:   time.Now(),
				Attempt:   handle.Attempt,
			}
			if enqErr := w.outbox.Enqueue(result); enqErr != nil {
				slog.Error("pipeline worker: persist timeout result failed", "task_id", result.TaskID, "err", enqErr)
			}
		}
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

// cancelRun stops all active jobs for the given run and reports any stop
// failures back to the caller so they can be surfaced to the master as a
// best-effort-remote-stop warning rather than silently swallowed.
func (w *Worker) cancelRun(runID string) error {
	w.mu.Lock()
	var toStop []trackedTask
	for _, tt := range w.active {
		if tt.runID != runID {
			continue
		}
		tt.cancel()
		if tt.starting {
			// No handle yet: canceling taskCtx is what interrupts the
			// in-progress driver.Start call. Mark it so dispatch's
			// post-Start check stops the workload instead of publishing it
			// as active — there is nothing to Stop here yet.
			tt.canceled = true
			continue
		}
		toStop = append(toStop, *tt)
	}
	w.mu.Unlock()

	// Stop the drivers using the handles captured under the lock.
	// Re-querying w.active would race with the observe goroutine's cleanup.
	var errs []error
	for _, tt := range toStop {
		if err := w.driver.Stop(context.Background(), tt.handle, 10*time.Second); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", tt.runID, err))
		}
	}
	return errors.Join(errs...)
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

// shutdown stops all in-flight jobs gracefully, bounded by ctx. It rejects
// any further dispatch() calls immediately (draining) so no new work is
// admitted while in-flight jobs are being stopped.
func (w *Worker) shutdown(ctx context.Context) {
	w.mu.Lock()
	w.draining = true
	handles := make([]pdriver.Handle, 0, len(w.active))
	for _, tt := range w.active {
		tt.cancel()
		if tt.starting {
			// No handle yet; canceling taskCtx is what interrupts the
			// in-progress driver.Start call (see dispatch's post-Start check).
			continue
		}
		handles = append(handles, tt.handle)
	}
	w.mu.Unlock()

	for _, h := range handles {
		_ = w.driver.Stop(ctx, h, 15*time.Second)
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
	env := make([]string, 0, 2)
	if user != "" {
		env = append(env, "PIPER_GIT_USER="+user)
	}
	if token != "" {
		env = append(env, "PIPER_GIT_TOKEN="+token)
	}
	return env
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
