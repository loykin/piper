package pipelineworker

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/internal/proto"
	pdriver "github.com/piper/piper/pkg/pipeline/worker/driver"
	k8sdriver "github.com/piper/piper/pkg/pipeline/worker/driver/k8s"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *grpcagent.Dispatcher

// StoreConfig holds the master connection and artifact store settings
// forwarded to K8s Job pods via piper agent exec arguments.
type StoreConfig struct {
	MasterURL    string
	WorkerToken  string
	StorageToken string
	StorageURL   string
}

// K8sConfig holds Kubernetes-specific driver and placement options.
type K8sConfig struct {
	Client               kubernetes.Interface
	Namespace            string   // primary namespace for Jobs
	Namespaces           []string // allowed namespaces (policy check)
	AgentImage           string   // piper binary image used in init containers
	AgentImagePullPolicy string
	DefaultImage         string
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
	// RenewLeases pushes active task IDs to the master for lease renewal.
	RenewLeases func([]string) error
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

	mu      sync.Mutex
	handles map[string]*trackedTask // runtimeKey → task
}

type trackedTask struct {
	handle pdriver.Handle
	cancel context.CancelFunc
}

func New(cfg Config) *Worker {
	driver, err := k8sdriver.New(k8sdriver.Config{
		WorkerID:             cfg.WorkerID,
		Namespace:            cfg.K8s.Namespace,
		Namespaces:           cfg.K8s.Namespaces,
		AgentImagePullPolicy: cfg.K8s.AgentImagePullPolicy,
		AgentImage:           pipelineWorkerImage(cfg),
		DefaultImage:         cfg.K8s.DefaultImage,
		TTLAfterFinished:     cfg.K8s.TTLAfterFinished,
		K8sClient:            cfg.K8s.Client,
	})
	return &Worker{
		cfg:        cfg,
		driver:     driver,
		observable: driver,
		runStopper: driver,
		initErr:    err,
		handles:    make(map[string]*trackedTask),
	}
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
	image, err := pdriver.ResolveImage(task, "k8s", a.cfg.K8s.DefaultImage)
	if err != nil {
		return err
	}
	namespace := pdriver.ResolveNamespace(task, a.cfg.K8s.Namespace)
	if len(a.cfg.K8s.Namespaces) > 0 && !slices.Contains(a.cfg.K8s.Namespaces, namespace) {
		return fmt.Errorf("k8s pipeline worker: namespace %q is not in the allowed list", namespace)
	}

	spec := pdriver.ExecSpec{
		RuntimeKey:   pdriver.RuntimeKey(a.cfg.WorkerID, task.RunID, task.StepName, task.Attempt),
		Image:        image,
		Namespace:    namespace,
		MasterURL:    a.cfg.Store.MasterURL,
		WorkerToken:  a.cfg.Store.WorkerToken,
		StorageToken: a.cfg.Store.StorageToken,
		StorageURL:   a.cfg.Store.StorageURL,
	}

	handle, err := a.driver.Start(ctx, task, spec)
	if err != nil {
		return err
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.handles[handle.RuntimeKey] = &trackedTask{handle: handle, cancel: cancel}
	a.mu.Unlock()

	go a.observe(waitCtx, handle)
	return nil
}

func (a *Worker) observe(ctx context.Context, handle pdriver.Handle) {
	defer func() {
		a.mu.Lock()
		if tracked := a.handles[handle.RuntimeKey]; tracked != nil {
			tracked.cancel()
		}
		delete(a.handles, handle.RuntimeKey)
		a.mu.Unlock()
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

	if a.runStopper == nil {
		return fmt.Errorf("pipeline driver does not support run cancellation")
	}
	if err := a.runStopper.StopRun(ctx, req.RunID, req.Namespace); err != nil {
		return err
	}
	return nil
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
			if len(ids) > 0 {
				_ = a.cfg.RenewLeases(ids)
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
