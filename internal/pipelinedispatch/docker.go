package pipelinedispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/directworker"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	dockerdriver "github.com/loykin/piper/pkg/pipeline/worker/driver/docker"
)

const localDockerWorkerID = "piper-docker-runtime"

// DockerBackendConfig configures direct, in-process Docker pipeline
// execution. No worker tunnel is involved.
type DockerBackendConfig struct {
	Network     string
	OutputDir   string
	Concurrency int
	// MasterURL/WorkerToken: file:// artifact storage is rewritten to this
	// HTTP endpoint — a Docker container cannot reach the host's local
	// filesystem directly, the same boundary problem k8s Job pods have.
	MasterURL   string
	WorkerToken string
	LogClient   logsink.PushClient
	Complete    func(proto.TaskResult) error
	RenewLeases func(workerID string, taskIDs []string)

	// Driver overrides the constructed dockerdriver.Driver. Test-only; nil
	// in production builds the real driver via dockerdriver.New.
	Driver pdriver.Driver
}

// DockerBackend adapts the existing Docker Start/Wait/Stop/Recover lifecycle
// to Queue's in-process ExecutionBackend contract, mirroring K8sBackend.
type DockerBackend struct {
	worker *directworker.Worker
	driver pdriver.Driver

	// Serialize dispatch with cancellation so a successful cancellation cannot
	// be followed by a late container creation from an already-scheduled call.
	mu       sync.Mutex
	canceled map[string]*time.Timer
}

func NewDockerBackend(cfg DockerBackendConfig) (*DockerBackend, error) {
	driver := cfg.Driver
	if driver == nil {
		d, err := dockerdriver.New(dockerdriver.Config{
			WorkerID:  localDockerWorkerID,
			ResultDir: filepath.Join(cfg.OutputDir, ".results"),
			OutputDir: cfg.OutputDir,
			Network:   cfg.Network,
		})
		if err != nil {
			return nil, fmt.Errorf("docker runtime backend: %w", err)
		}
		driver = d
	}

	w, err := directworker.New(directworker.Config{
		WorkerID:    localDockerWorkerID,
		Driver:      driver,
		Concurrency: cfg.Concurrency,
		OutputDir:   cfg.OutputDir,
		ResolveImage: func(task *proto.Task) (string, error) {
			return pdriver.ResolveImage(task, "docker")
		},
		ResolveStorage: func(task *proto.Task) (string, string) {
			return taskStorageForDirectRuntime(task, cfg.MasterURL, cfg.WorkerToken)
		},
		LogClient:    cfg.LogClient,
		ReportResult: cfg.Complete,
		RenewLeases: func(taskIDs []string) error {
			if cfg.RenewLeases != nil {
				cfg.RenewLeases(localDockerWorkerID, taskIDs)
			}
			return nil
		},
	})
	if err != nil {
		if closer, ok := driver.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return nil, err
	}
	return &DockerBackend{worker: w, driver: driver, canceled: make(map[string]*time.Timer)}, nil
}

func (b *DockerBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	if b == nil || b.worker == nil {
		return fmt.Errorf("docker runtime backend is not configured")
	}
	if err := validateDirectPlacement(task, "docker runtime", "docker"); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, canceled := b.canceled[task.RunID]; canceled {
		return fmt.Errorf("docker runtime dispatch: run %s was canceled before dispatch", task.RunID)
	}
	err := b.worker.Dispatch(ctx, task)
	var busy *iagent.BusyError
	if errors.As(err, &busy) {
		return &DispatchError{Retryable: true, Err: err}
	}
	return err
}

func (b *DockerBackend) CancelRun(ctx context.Context, runID string) error {
	if b == nil || b.worker == nil || runID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.markCanceledLocked(runID)
	return b.worker.CancelRun(ctx, runID)
}

func (b *DockerBackend) ReleaseRun(runID string) {
	b.mu.Lock()
	if timer := b.canceled[runID]; timer != nil {
		timer.Stop()
		delete(b.canceled, runID)
	}
	b.mu.Unlock()
}

// Observe recovers containers from a previous Piper process and renews leases
// until ctx is canceled.
func (b *DockerBackend) Observe(ctx context.Context) {
	if b != nil && b.worker != nil {
		b.worker.Observe(ctx)
	}
}

// Close releases the underlying Docker daemon client connection. Unlike
// kubernetes.Interface, a real dockerclient.APIClient holds an open
// connection that must be closed on shutdown.
func (b *DockerBackend) Close() error {
	if b == nil || b.driver == nil {
		return nil
	}
	if closer, ok := b.driver.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (b *DockerBackend) markCanceledLocked(runID string) {
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

// taskStorageForDirectRuntime rewrites file:// artifact storage to an HTTP
// endpoint reachable from inside a container, mirroring
// internal/k8sworker/pipeline/worker.go's taskStorageForK8sWorker (k8s Job
// pods cannot reach the host's local filesystem directly either).
func taskStorageForDirectRuntime(task *proto.Task, masterURL, workerToken string) (storageURL, storageToken string) {
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

var (
	_ ExecutionBackend  = (*DockerBackend)(nil)
	_ CancelableBackend = (*DockerBackend)(nil)
	_ RunOwner          = (*DockerBackend)(nil)
)
