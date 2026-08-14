package pipelinedispatch

import (
	"context"
	"fmt"
	"sync"
	"time"

	pipelineworker "github.com/loykin/piper/internal/k8sworker/pipeline"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/proto"
	"k8s.io/client-go/kubernetes"
)

const localK8sWorkerID = "piper-k8s-runtime"

// K8sBackendConfig configures direct, in-process Kubernetes pipeline
// execution. The client is expected to target the cluster owned by this Piper
// installation; no worker tunnel is involved.
type K8sBackendConfig struct {
	Context             context.Context
	Client              kubernetes.Interface
	Namespaces          []string
	PipelineRunnerImage string
	ImagePullPolicy     string
	TTLAfterFinished    *int32
	MasterURL           string
	WorkloadToken       string
	LogClient           logsink.PushClient
	Complete            func(proto.TaskResult) error
	RenewLeases         func(workerID string, taskIDs []string)
}

// K8sBackend adapts the existing Kubernetes Start/Wait/Stop/Recover lifecycle
// to Queue's in-process ExecutionBackend contract.
type K8sBackend struct {
	worker *pipelineworker.Worker

	// Serialize dispatch with cancellation so a successful cancellation cannot
	// be followed by a late Job creation from an already-scheduled goroutine.
	mu       sync.Mutex
	canceled map[string]*time.Timer
}

func NewK8sBackend(cfg K8sBackendConfig) (*K8sBackend, error) {
	w := pipelineworker.New(pipelineworker.Config{
		WorkerID: localK8sWorkerID,
		Context:  cfg.Context,
		Store: pipelineworker.StoreConfig{
			MasterURL:     cfg.MasterURL,
			WorkloadToken: cfg.WorkloadToken,
		},
		K8s: pipelineworker.K8sConfig{
			Client:               cfg.Client,
			Namespaces:           append([]string(nil), cfg.Namespaces...),
			AgentImage:           cfg.PipelineRunnerImage,
			AgentImagePullPolicy: cfg.ImagePullPolicy,
			TTLAfterFinished:     cfg.TTLAfterFinished,
		},
		ReportResult: cfg.Complete,
		RenewLeases: func(taskIDs []string) error {
			if cfg.RenewLeases != nil {
				cfg.RenewLeases(localK8sWorkerID, taskIDs)
			}
			return nil
		},
		LogClient: cfg.LogClient,
	})
	if err := w.InitError(); err != nil {
		return nil, err
	}
	return &K8sBackend{worker: w, canceled: make(map[string]*time.Timer)}, nil
}

func (b *K8sBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	if b == nil || b.worker == nil {
		return fmt.Errorf("k8s runtime backend is not configured")
	}
	if err := validateDirectK8sTask(task); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, canceled := b.canceled[task.RunID]; canceled {
		return fmt.Errorf("k8s runtime dispatch: run %s was canceled before dispatch", task.RunID)
	}
	return b.worker.Dispatch(ctx, task)
}

func (b *K8sBackend) CancelRun(ctx context.Context, runID string) error {
	if b == nil || b.worker == nil || runID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.markCanceledLocked(runID)
	return b.worker.CancelRun(ctx, runID)
}

func (b *K8sBackend) ReleaseRun(runID string) {
	b.mu.Lock()
	if timer := b.canceled[runID]; timer != nil {
		timer.Stop()
		delete(b.canceled, runID)
	}
	b.mu.Unlock()
}

// Observe recovers Jobs from a previous Piper process and reconciles terminal
// state until ctx is canceled.
func (b *K8sBackend) Observe(ctx context.Context) {
	if b != nil && b.worker != nil {
		b.worker.Observe(ctx)
	}
}

func (b *K8sBackend) markCanceledLocked(runID string) {
	if old := b.canceled[runID]; old != nil {
		old.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(time.Minute, func() {
		b.mu.Lock()
		if b.canceled[runID] == timer {
			delete(b.canceled, runID)
		}
		b.mu.Unlock()
	})
	b.canceled[runID] = timer
}

func validateDirectK8sTask(task *proto.Task) error {
	return validateDirectPlacement(task, "k8s runtime", "k8s")
}

var _ ExecutionBackend = (*K8sBackend)(nil)
var _ CancelableBackend = (*K8sBackend)(nil)
var _ RunOwner = (*K8sBackend)(nil)
