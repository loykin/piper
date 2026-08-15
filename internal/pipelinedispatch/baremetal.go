package pipelinedispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/directruntime"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/pipelinedriver"
	baremetaldriver "github.com/loykin/piper/pkg/pipeline/pipelinedriver/baremetal"
)

const localBaremetalRuntimeID = "piper-baremetal-runtime"

// BaremetalBackendConfig configures direct, in-process baremetal (subprocess)
// pipeline execution. No worker tunnel is involved.
type BaremetalBackendConfig struct {
	MetaDir     string // directory for metadata + PID sidecar files; default: $TMPDIR/piper-meta
	OutputDir   string
	Concurrency int
	// RemoteStore is true when using S3 or other remote artifact store, for
	// best-effort crash-residue cleanup logging in the baremetal driver.
	RemoteStore bool
	LogClient   logsink.PushClient
	Complete    func(proto.TaskResult) error
	RenewLeases func(runtimeID string, taskIDs []string)

	// Driver overrides the constructed baremetaldriver.Driver. Test-only; nil
	// in production builds the real driver via baremetaldriver.New.
	Driver pdriver.Driver
}

// BaremetalBackend adapts the existing baremetal Start/Wait/Stop/Recover
// lifecycle to Queue's in-process ExecutionBackend contract, mirroring
// K8sBackend/DockerBackend.
type BaremetalBackend struct {
	runtime *directruntime.Runtime
	driver  pdriver.Driver

	mu       sync.Mutex
	canceled map[string]*time.Timer
}

func NewBaremetalBackend(cfg BaremetalBackendConfig) (*BaremetalBackend, error) {
	driver := cfg.Driver
	if driver == nil {
		d, err := baremetaldriver.New(baremetaldriver.Config{
			RuntimeID:   localBaremetalRuntimeID,
			MetaDir:     cfg.MetaDir,
			RemoteStore: cfg.RemoteStore,
		})
		if err != nil {
			return nil, fmt.Errorf("baremetal runtime backend: %w", err)
		}
		driver = d
	}

	w, err := directruntime.New(directruntime.Config{
		RuntimeID:   localBaremetalRuntimeID,
		Driver:      driver,
		Concurrency: cfg.Concurrency,
		OutputDir:   cfg.OutputDir,
		// No ResolveImage: baremetal subprocesses have no image concept.
		ResolveStorage: func(task *proto.Task) (string, string) {
			// Baremetal shares the host filesystem directly (same host,
			// real paths) — no file:// -> HTTP rewrite needed, unlike
			// docker/k8s. Mirrors the existing server.local/embedded-worker
			// precedent's LocalStoreAccess:true treatment for baremetal.
			if task == nil {
				return "", ""
			}
			return task.StorageURL, task.StorageToken
		},
		LogClient:    cfg.LogClient,
		ReportResult: cfg.Complete,
		RenewLeases: func(taskIDs []string) error {
			if cfg.RenewLeases != nil {
				cfg.RenewLeases(localBaremetalRuntimeID, taskIDs)
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &BaremetalBackend{runtime: w, driver: driver, canceled: make(map[string]*time.Timer)}, nil
}

func (b *BaremetalBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	if b == nil || b.runtime == nil {
		return fmt.Errorf("baremetal runtime backend is not configured")
	}
	if err := validateDirectPlacement(task, "baremetal runtime", "baremetal"); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, canceled := b.canceled[task.RunID]; canceled {
		return fmt.Errorf("baremetal runtime dispatch: run %s was canceled before dispatch", task.RunID)
	}
	err := b.runtime.Dispatch(ctx, task)
	var busy *iagent.BusyError
	if errors.As(err, &busy) {
		return &DispatchError{Retryable: true, Err: err}
	}
	return err
}

func (b *BaremetalBackend) CancelRun(ctx context.Context, runID string) error {
	if b == nil || b.runtime == nil || runID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.markCanceledLocked(runID)
	return b.runtime.CancelRun(ctx, runID)
}

func (b *BaremetalBackend) ReleaseRun(runID string) {
	b.mu.Lock()
	if timer := b.canceled[runID]; timer != nil {
		timer.Stop()
		delete(b.canceled, runID)
	}
	b.mu.Unlock()
}

// Observe recovers processes from a previous Piper process and renews leases
// until ctx is canceled.
func (b *BaremetalBackend) Observe(ctx context.Context) {
	if b != nil && b.runtime != nil {
		b.runtime.Observe(ctx)
	}
}

func (b *BaremetalBackend) markCanceledLocked(runID string) {
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

var (
	_ ExecutionBackend  = (*BaremetalBackend)(nil)
	_ CancelableBackend = (*BaremetalBackend)(nil)
	_ RunOwner          = (*BaremetalBackend)(nil)
)
