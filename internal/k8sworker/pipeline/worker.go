package pipelineworker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/grpcagent"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	k8sdriver "github.com/loykin/piper/pkg/pipeline/worker/driver/k8s"
	"github.com/loykin/piper/pkg/pipeline/worker/scheduler"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *grpcagent.Dispatcher

// StoreConfig holds the master connection and artifact store settings
// forwarded to K8s Job pods via piper agent exec arguments.
type StoreConfig struct {
	MasterURL   string
	WorkerToken string
}

// K8sConfig holds Kubernetes-specific driver and placement options.
type K8sConfig struct {
	Client               kubernetes.Interface
	Namespaces           []string // allowed namespaces (policy check)
	AgentImage           string   // piper binary image used in init containers
	AgentImagePullPolicy string
	TTLAfterFinished     *int32
}

// Config holds K8s pipeline worker configuration grouped by layer.
type Config struct {
	WorkerID string
	Store    StoreConfig
	K8s      K8sConfig
	// ReportResult is called with the final TaskResult for each completed step.
	// Typically enqueues into a ResultOutbox for durable delivery.
	ReportResult func(proto.TaskResult) error
	// RenewLeases pushes active task IDs and Registry-owned run IDs to the
	// master for lease renewal — the latter also covers a run between steps
	// (e.g. mid retry-delay) with no currently-active task, so the master's
	// staleness sweep doesn't mistake it for abandoned.
	RenewLeases func(taskIDs, runIDs []string) error
	LogClient   logsink.PushClient
	// RequestOutbox durably delivers the worker-owned DAG scheduler's
	// (pkg/pipeline/worker/scheduler) pipeline.step_upsert/run_finalize
	// reports for runs received via pipeline.run_dispatch. Required
	// whenever the caller wants that (still additive, not yet reachable in
	// production) path to work — see internal/k8sworker/worker.go, which
	// constructs it the same way it already does for ReportResult's
	// ResultOutbox.
	RequestOutbox *pdriver.RequestOutbox
	// MaxAttempts and RetryDelay configure the scheduler's retry policy.
	// MaxAttempts <1 means 1 (no retries). See
	// pkg/pipeline/worker/worker.go's AgentConfig for the baremetal/docker
	// equivalent.
	MaxAttempts int
	RetryDelay  time.Duration
}

// Worker manages K8s pipeline workloads dispatched via gRPC.
// It uses K8sDriver to satisfy the pdriver.Driver interface, making
// K8s execution share the same lifecycle contract as baremetal/docker.
type Worker struct {
	cfg        Config
	driver     pdriver.Driver
	observable pdriver.Observable
	runStopper pdriver.RunStopper
	initErr    error
	// registry owns the worker-side DAG scheduler (pkg/pipeline/worker/scheduler)
	// for runs received via pipeline.run_dispatch. Additive alongside the
	// per-step pipeline.dispatch path above — see docs/backend/develop.md's
	// State Ownership section: nothing on the master sends run_dispatch yet.
	registry *scheduler.Registry

	mu      sync.Mutex
	handles map[string]*trackedTask // runtimeKey → task
}

type trackedTask struct {
	handle pdriver.Handle
	cancel context.CancelFunc
	logs   logsink.LogSink
}

func New(cfg Config) *Worker {
	driver, err := k8sdriver.New(k8sdriver.Config{
		WorkerID:             cfg.WorkerID,
		Namespaces:           cfg.K8s.Namespaces,
		AgentImagePullPolicy: cfg.K8s.AgentImagePullPolicy,
		AgentImage:           pipelineWorkerImage(cfg),
		TTLAfterFinished:     cfg.K8s.TTLAfterFinished,
		K8sClient:            cfg.K8s.Client,
	})
	w := &Worker{
		cfg:        cfg,
		driver:     driver,
		observable: driver,
		runStopper: driver,
		initErr:    err,
		handles:    make(map[string]*trackedTask),
	}
	w.registry = scheduler.NewRegistry(scheduler.RegistryOptions{
		Driver:        driver,
		BuildExecSpec: w.buildExecSpec,
		// Live pod log streaming needs the k8s API client and the exact
		// namespace/job-name chosen for this step — neither fits
		// driver.Driver's infra-agnostic Handle/Exit contract, so it's
		// wired here via AfterStart rather than inside k8sdriver itself.
		// Mirrors dispatchPipeline's own `go streamJobLogs(...)` call below
		// exactly, just reached through the scheduler instead.
		AfterStart: func(stepCtx context.Context, task *proto.Task, spec pdriver.ExecSpec, handle pdriver.Handle) {
			if spec.LogSink != nil {
				go streamJobLogs(stepCtx, cfg.K8s.Client, spec.Namespace, handle.RuntimeKey, task, spec.LogSink)
			}
		},
		BuildReporter: func(projectID, runID string) scheduler.StepReporter {
			return scheduler.NewOutboxReporter(cfg.RequestOutbox, projectID, runID)
		},
		MaxAttempts: cfg.MaxAttempts,
		RetryDelay:  cfg.RetryDelay,
		WorkerID:    cfg.WorkerID,
	})
	return w
}

func Register(dispatcher Dispatcher, cfg Config) *Worker {
	w := New(cfg)
	w.register(dispatcher)
	return w
}

type pipelineCancelRunRequest struct {
	RunID     string `json:"run_id"`
	Namespace string `json:"namespace,omitempty"`
}

func (a *Worker) register(dispatcher Dispatcher) {
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodPipelineDispatch, func(ctx context.Context, task proto.Task) (any, error) {
		return nil, a.dispatchPipeline(ctx, &task)
	})
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodPipelineRunDispatch, func(_ context.Context, dispatch proto.RunDispatch) (any, error) {
		return nil, a.registry.StartRun(dispatch)
	})
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodPipelineCancelRun, func(ctx context.Context, req pipelineCancelRunRequest) (any, error) {
		return nil, a.cancelPipelineRun(ctx, req)
	})
}

func (a *Worker) dispatchPipeline(ctx context.Context, task *proto.Task) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}
	if a.initErr != nil {
		return a.initErr
	}

	// Resolve image and namespace here (worker layer), not inside the driver.
	image, err := pdriver.ResolveImage(task, "k8s")
	if err != nil {
		return err
	}
	namespace, err := pdriver.ResolveNamespace(task)
	if err != nil {
		return err
	}
	if len(a.cfg.K8s.Namespaces) > 0 && !slices.Contains(a.cfg.K8s.Namespaces, namespace) {
		return fmt.Errorf("k8s pipeline worker: namespace %q is not in the allowed list", namespace)
	}

	storageURL, storageToken := taskStorageForK8sWorker(task, a.cfg.Store.MasterURL, a.cfg.Store.WorkerToken)
	execEnv := append([]string{}, task.Env...)
	spec := pdriver.ExecSpec{
		RuntimeKey:   pdriver.RuntimeKey(a.cfg.WorkerID, task.RunID, task.StepName, task.Attempt),
		Image:        image,
		Namespace:    namespace,
		StorageToken: storageToken,
		StorageURL:   storageURL,
	}
	if a.cfg.LogClient != nil {
		spec.LogSink = logsink.NewRedactingSink(logsink.NewGRPCLogSink(task.ProjectID, a.cfg.LogClient), logsink.ValuesFromEnv(execEnv))
	}

	handle, err := a.driver.Start(ctx, task, spec)
	if err != nil {
		if spec.LogSink != nil {
			spec.LogSink.Stop()
		}
		return err
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.handles[handle.RuntimeKey] = &trackedTask{handle: handle, cancel: cancel, logs: spec.LogSink}
	a.mu.Unlock()
	if spec.LogSink != nil {
		go streamJobLogs(waitCtx, a.cfg.K8s.Client, spec.Namespace, handle.RuntimeKey, task, spec.LogSink)
	}

	go a.observe(waitCtx, handle)
	return nil
}

// buildExecSpec builds the driver.ExecSpec for task — the scheduler.BuildExecSpec
// hook pkg/pipeline/worker/scheduler's RunScheduler uses for runs received
// via pipeline.run_dispatch. Mirrors the ExecSpec construction inlined in
// dispatchPipeline above (the per-step pipeline.dispatch path) so both
// paths resolve image/namespace/storage/log-sink identically. Live pod log
// streaming (dispatchPipeline's streamJobLogs) is wired separately, via the
// Registry's AfterStart hook in New() — see the comment there.
func (a *Worker) buildExecSpec(task *proto.Task) (pdriver.ExecSpec, error) {
	image, err := pdriver.ResolveImage(task, "k8s")
	if err != nil {
		return pdriver.ExecSpec{}, err
	}
	namespace, err := pdriver.ResolveNamespace(task)
	if err != nil {
		return pdriver.ExecSpec{}, err
	}
	if len(a.cfg.K8s.Namespaces) > 0 && !slices.Contains(a.cfg.K8s.Namespaces, namespace) {
		return pdriver.ExecSpec{}, fmt.Errorf("k8s pipeline worker: namespace %q is not in the allowed list", namespace)
	}

	storageURL, storageToken := taskStorageForK8sWorker(task, a.cfg.Store.MasterURL, a.cfg.Store.WorkerToken)
	execEnv := append([]string{}, task.Env...)
	spec := pdriver.ExecSpec{
		RuntimeKey:   pdriver.RuntimeKey(a.cfg.WorkerID, task.RunID, task.StepName, task.Attempt),
		Image:        image,
		Namespace:    namespace,
		StorageToken: storageToken,
		StorageURL:   storageURL,
	}
	if a.cfg.LogClient != nil {
		spec.LogSink = logsink.NewRedactingSink(logsink.NewGRPCLogSink(task.ProjectID, a.cfg.LogClient), logsink.ValuesFromEnv(execEnv))
	}
	return spec, nil
}

func taskStorageForK8sWorker(task *proto.Task, masterURL, workerToken string) (storageURL, storageToken string) {
	if task == nil {
		return "", ""
	}
	storageURL = task.StorageURL
	storageToken = task.StorageToken
	if strings.HasPrefix(storageURL, "file://") {
		storageURL = strings.TrimRight(strings.TrimSpace(masterURL), "/") + "/store"
		if storageToken == "" {
			storageToken = workerToken
		}
	}
	return storageURL, storageToken
}

func (a *Worker) observe(ctx context.Context, handle pdriver.Handle) {
	defer func() {
		a.mu.Lock()
		tracked := a.handles[handle.RuntimeKey]
		if tracked != nil {
			tracked.cancel()
		}
		delete(a.handles, handle.RuntimeKey)
		a.mu.Unlock()
		if tracked != nil && tracked.logs != nil {
			tracked.logs.Stop()
		}
	}()

	exit, err := a.driver.Wait(ctx, handle)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("k8s pipeline: wait failed", "task_id", handle.TaskID, "err", err)
		}
		return
	}

	if a.cfg.ReportResult == nil {
		return
	}

	result := buildResult(handle, exit)
	if result.WorkerID == "" {
		result.WorkerID = a.cfg.WorkerID
	}
	if err := a.cfg.ReportResult(result); err != nil {
		slog.Warn("k8s pipeline: report result failed", "task_id", handle.TaskID, "err", err)
	}
}

func streamJobLogs(ctx context.Context, client kubernetes.Interface, namespace, jobName string, task *proto.Task, sink logsink.LogSink) {
	if client == nil || sink == nil {
		return
	}
	var podName string
	for podName == "" {
		pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
		if err == nil && len(pods.Items) > 0 {
			podName = pods.Items[0].Name
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	stream, err := client.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Follow: true}).Stream(ctx)
	if err != nil {
		return
	}
	defer func() { _ = stream.Close() }()
	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		sink.Append(task.RunID, task.StepName, "combined", scanner.Text(), time.Now())
	}
}

func (a *Worker) cancelPipelineRun(ctx context.Context, req pipelineCancelRunRequest) error {
	if req.RunID == "" {
		return nil
	}
	a.mu.Lock()
	for _, tracked := range a.handles {
		if tracked.handle.RunID == req.RunID {
			tracked.cancel()
		}
	}
	a.mu.Unlock()

	var errs []error
	if a.runStopper == nil {
		errs = append(errs, fmt.Errorf("pipeline driver does not support run cancellation"))
	} else if err := a.runStopper.StopRun(ctx, req.RunID, req.Namespace); err != nil {
		errs = append(errs, err)
	}
	// Also forward to the scheduler-owned path (pipeline.run_dispatch) —
	// no-ops if req.RunID isn't a run its Registry currently tracks. The
	// two paths are mutually exclusive per run today, so exactly one of
	// them ever has real work to do for a given cancel.
	if err := a.registry.CancelRun(req.RunID); err != nil {
		errs = append(errs, fmt.Errorf("scheduler cancel: %w", err))
	}
	return errors.Join(errs...)
}

// Observe runs the K8s Job reconcile loop (implements the outer Observe contract).
// The K8s main worker calls this in a background goroutine.
func (a *Worker) Observe(ctx context.Context) {
	if a.initErr != nil {
		slog.Error("k8s pipeline: driver initialization failed", "err", a.initErr)
		return
	}
	// Recover any jobs from before worker restart.
	handles, err := a.driver.Recover(ctx)
	if err != nil {
		slog.Warn("k8s pipeline: recover failed", "err", err)
	}
	for _, h := range handles {
		waitCtx, cancel := context.WithCancel(ctx)
		a.mu.Lock()
		a.handles[h.RuntimeKey] = &trackedTask{handle: h, cancel: cancel}
		a.mu.Unlock()
		go a.observe(waitCtx, h)
	}

	// Renew leases on recovered + active tasks.
	go a.leaseLoop(ctx)

	// Drive the K8s reconcile loop (signals Wait channels).
	if a.observable != nil {
		a.observable.Observe(ctx)
	}
}

func (a *Worker) leaseLoop(ctx context.Context) {
	if a.cfg.RenewLeases == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			ids := make([]string, 0, len(a.handles))
			for _, tracked := range a.handles {
				ids = append(ids, tracked.handle.TaskID)
			}
			a.mu.Unlock()
			runIDs := a.registry.RunIDs()
			if len(ids) > 0 || len(runIDs) > 0 {
				_ = a.cfg.RenewLeases(ids, runIDs)
			}
		}
	}
}

// buildResult constructs a TaskResult from a Driver Exit.
func buildResult(handle pdriver.Handle, exit pdriver.Exit) proto.TaskResult {
	if exit.Result != nil {
		return *exit.Result
	}
	if exit.InfraFailure != nil {
		return proto.TaskResult{
			TaskID:  handle.TaskID,
			Status:  proto.TaskStatusFailed,
			Error:   exit.InfraFailure.Error(),
			EndedAt: time.Now(),
			Attempt: handle.Attempt,
		}
	}
	return proto.TaskResult{
		TaskID:  handle.TaskID,
		Status:  proto.TaskStatusFailed,
		Error:   "k8s job result unavailable",
		EndedAt: time.Now(),
		Attempt: handle.Attempt,
	}
}

// ActiveTaskIDs returns task IDs currently tracked by this worker.
func (a *Worker) ActiveTaskIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]string, 0, len(a.handles))
	for _, tracked := range a.handles {
		ids = append(ids, tracked.handle.TaskID)
	}
	return ids
}

// pipelineWorkerImage returns the piper agent image used in Job init containers.
func pipelineWorkerImage(cfg Config) string {
	if cfg.K8s.AgentImage != "" {
		return cfg.K8s.AgentImage
	}
	return "piper/piper:latest"
}
