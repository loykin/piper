// Package directworker implements a driver-agnostic, in-process pipeline
// worker shared by the Docker and baremetal direct runtime backends
// (internal/pipelinedispatch.DockerBackend/BaremetalBackend). It has no gRPC
// tunnel, no result outbox, and no lease renewal over a tunnel: it drives a
// pkg/pipeline/worker/driver.Driver's Start/Wait/Stop/Recover lifecycle
// directly and reports results/leases through injected callbacks, mirroring
// internal/k8sworker/pipeline.Worker's shape rather than the remote
// pkg/pipeline/worker.Worker's.
package directworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/proto"
	agentutil "github.com/loykin/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

// Config configures a Worker. Unlike the direct K8s path (which relies on
// the Kubernetes cluster scheduler to bound concurrent Jobs and therefore
// has no admission gate at all), Docker/baremetal run directly on the Piper
// host, so Concurrency is required here.
type Config struct {
	WorkerID    string
	Driver      pdriver.Driver
	Concurrency int
	OutputDir   string
	// ResolveImage resolves the image for a task. nil when the runtime has
	// no image concept (baremetal).
	ResolveImage func(*proto.Task) (string, error)
	// ResolveStorage resolves the storage URL/token a step should use.
	// Runtime-specific policy (e.g. docker rewrites file:// to an HTTP
	// endpoint the same way k8s Job pods must; baremetal passes task
	// storage through unchanged since it shares the host filesystem) —
	// injected so this package stays policy-free. Required.
	ResolveStorage func(task *proto.Task) (storageURL, storageToken string)
	LogClient      logsink.PushClient
	// ReportResult is called with the final TaskResult for each completed step.
	ReportResult func(proto.TaskResult) error
	// RenewLeases pushes active task IDs for lease renewal. May be nil.
	RenewLeases func(taskIDs []string) error
}

// trackedTask mirrors pkg/pipeline/worker/worker.go's trackedTask: registered
// (starting=true, no handle yet) atomically with the capacity reservation,
// before the potentially-slow driver.Start call, so a CancelRun arriving
// mid-Start can find and cancel it instead of finding nothing.
type trackedTask struct {
	runID    string
	handle   pdriver.Handle
	cancel   context.CancelFunc
	logs     logsink.LogSink
	starting bool
	canceled bool
}

// Worker is a driver-agnostic in-process pipeline worker.
type Worker struct {
	cfg Config

	mu       sync.Mutex
	active   map[string]*trackedTask // runtimeKey → trackedTask
	inFlight int
	draining bool
}

// New validates cfg and constructs a Worker. It does not construct or own
// cfg.Driver — callers (the Docker/baremetal backends) build and close it.
func New(cfg Config) (*Worker, error) {
	if cfg.WorkerID == "" {
		return nil, fmt.Errorf("directworker: WorkerID is required")
	}
	if cfg.Driver == nil {
		return nil, fmt.Errorf("directworker: Driver is required")
	}
	if cfg.Concurrency <= 0 {
		return nil, fmt.Errorf("directworker: Concurrency must be > 0")
	}
	if cfg.ResolveStorage == nil {
		return nil, fmt.Errorf("directworker: ResolveStorage is required")
	}
	return &Worker{cfg: cfg, active: make(map[string]*trackedTask)}, nil
}

// Dispatch starts a pipeline task directly via the configured driver.
func (w *Worker) Dispatch(ctx context.Context, task *proto.Task) error {
	if task == nil {
		return fmt.Errorf("directworker: task is required")
	}
	runtimeKey := pdriver.RuntimeKey(w.cfg.WorkerID, task.RunID, task.StepName, task.Attempt)

	var taskCtx context.Context
	var cancel context.CancelFunc
	if task.Deadline != nil && !task.Deadline.IsZero() {
		taskCtx, cancel = context.WithDeadline(context.Background(), *task.Deadline)
	} else {
		taskCtx, cancel = context.WithCancel(context.Background())
	}

	// Reserve capacity and a "starting" placeholder atomically with the
	// capacity check, before the potentially-slow driver.Start call below —
	// see pkg/pipeline/worker/worker.go's dispatch() for the race this closes.
	w.mu.Lock()
	if w.draining {
		w.mu.Unlock()
		cancel()
		return &iagent.BusyError{Reason: "worker draining"}
	}
	if w.inFlight >= w.cfg.Concurrency {
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

	storageURL, storageToken := w.cfg.ResolveStorage(task)
	spec := pdriver.ExecSpec{
		RuntimeKey:   runtimeKey,
		OutputDir:    w.cfg.OutputDir,
		StorageToken: storageToken,
		StorageURL:   storageURL,
	}
	if w.cfg.LogClient != nil {
		spec.LogSink = logsink.NewRedactingSink(logsink.NewGRPCLogSink(task.ProjectID, w.cfg.LogClient), logsink.ValuesFromEnv(task.Env))
	}
	if w.cfg.ResolveImage != nil {
		image, err := w.cfg.ResolveImage(task)
		if err != nil {
			rollback()
			if spec.LogSink != nil {
				spec.LogSink.Stop()
			}
			return err
		}
		spec.Image = image
	}

	handle, err := w.cfg.Driver.Start(taskCtx, task, spec)
	if err != nil {
		rollback()
		if spec.LogSink != nil {
			spec.LogSink.Stop()
		}
		return fmt.Errorf("start job: %w", err)
	}

	w.mu.Lock()
	tt, stillTracked := w.active[runtimeKey]
	canceledMidStart := !stillTracked || tt.canceled
	if canceledMidStart {
		// CancelRun marked this "starting" entry canceled (or, defensively,
		// it's altogether missing) while Start was in flight. Stop what was
		// just started instead of publishing it as active.
		delete(w.active, runtimeKey)
		w.inFlight--
		w.mu.Unlock()
		_ = w.cfg.Driver.Stop(context.Background(), handle, 10*time.Second)
		if spec.LogSink != nil {
			spec.LogSink.Stop()
		}
		return nil
	}
	tt.handle = handle
	tt.logs = spec.LogSink
	tt.starting = false
	w.mu.Unlock()

	go w.observe(taskCtx, handle)
	return nil
}

// CancelRun stops all active jobs for the given run. Unlike the direct K8s
// path (whose CancelRun additionally scans the cluster by namespace via
// pdriver.RunStopper, catching Jobs not yet tracked in worker memory),
// neither dockerdriver.Driver nor baremetaldriver.Driver implements
// RunStopper, so this only covers tasks already tracked in memory. Observe's
// startup Recover() call re-populates that map from persisted state before
// any new dispatch, so a restart-then-cancel sequence is still covered.
func (w *Worker) CancelRun(_ context.Context, runID string) error {
	if runID == "" {
		return nil
	}
	w.mu.Lock()
	var toStop []trackedTask
	for _, tt := range w.active {
		if tt.runID != runID {
			continue
		}
		tt.cancel()
		if tt.starting {
			tt.canceled = true
			continue
		}
		toStop = append(toStop, *tt)
	}
	w.mu.Unlock()

	var errs []error
	for _, tt := range toStop {
		if err := w.cfg.Driver.Stop(context.Background(), tt.handle, 10*time.Second); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", tt.runID, err))
		}
	}
	return errors.Join(errs...)
}

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

	exit, err := w.cfg.Driver.Wait(ctx, handle)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("directworker: wait failed", "task_id", handle.TaskID, "err", err)
		}
		return
	}

	if w.cfg.ReportResult == nil {
		return
	}
	result := w.buildResult(handle, exit)
	if err := w.cfg.ReportResult(result); err != nil {
		slog.Warn("directworker: report result failed", "task_id", handle.TaskID, "err", err)
	}
}

// buildResult mirrors pkg/pipeline/worker/worker.go's buildResult (the fuller
// version, not internal/k8sworker/pipeline's — Docker/baremetal both use
// exit.ResultPath, unlike K8s which always parses the result itself).
func (w *Worker) buildResult(handle pdriver.Handle, exit pdriver.Exit) proto.TaskResult {
	if exit.Result != nil {
		r := *exit.Result
		r.WorkerID = w.cfg.WorkerID
		return r
	}

	if exit.InfraFailure != nil {
		return proto.TaskResult{
			TaskID:    handle.TaskID,
			WorkerID:  w.cfg.WorkerID,
			Status:    proto.TaskStatusFailed,
			Error:     exit.InfraFailure.Error(),
			StartedAt: time.Now(),
			EndedAt:   time.Now(),
			Attempt:   handle.Attempt,
		}
	}

	if exit.ResultPath != "" {
		if data, err := os.ReadFile(exit.ResultPath); err == nil {
			if r, err := agentutil.ReadAgentResult(data); err == nil {
				r.WorkerID = w.cfg.WorkerID
				return r
			}
		}
	}

	return proto.TaskResult{
		TaskID:   handle.TaskID,
		WorkerID: w.cfg.WorkerID,
		Status:   proto.TaskStatusFailed,
		Error:    "result unavailable after job completion",
		EndedAt:  time.Now(),
		Attempt:  handle.Attempt,
	}
}

// Observe recovers state from a previous process, re-registers any
// still-running handles, and blocks running lease renewal until ctx is
// canceled. Unlike internal/k8sworker/pipeline.Worker.Observe, it never
// calls a driver Observable reconcile loop: neither dockerdriver.Driver nor
// baremetaldriver.Driver implements pdriver.Observable — both Wait() calls
// block synchronously on the real process/container (confirmed by the
// fed.md 13.1 driver contract-test pass), so there is nothing to poll.
func (w *Worker) Observe(ctx context.Context) {
	handles, err := w.cfg.Driver.Recover(ctx)
	if err != nil {
		slog.Warn("directworker: recover failed", "err", err)
	}
	for _, h := range handles {
		taskCtx, cancel := context.WithCancel(ctx)
		w.mu.Lock()
		w.active[h.RuntimeKey] = &trackedTask{runID: h.RunID, handle: h, cancel: cancel}
		w.inFlight++
		w.mu.Unlock()
		go w.observe(taskCtx, h)
	}
	if len(handles) > 0 {
		slog.Info("directworker: recovered jobs", "count", len(handles))
	}

	w.leaseLoop(ctx)
}

func (w *Worker) leaseLoop(ctx context.Context) {
	if w.cfg.RenewLeases == nil {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.mu.Lock()
			ids := make([]string, 0, len(w.active))
			for _, tt := range w.active {
				ids = append(ids, tt.handle.TaskID)
			}
			w.mu.Unlock()
			if len(ids) > 0 {
				_ = w.cfg.RenewLeases(ids)
			}
		}
	}
}
