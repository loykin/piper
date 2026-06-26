// Package k8s implements RuntimeDriver for Kubernetes Job execution.
// It wraps pkg/k8s.Launcher and exposes the driver.Driver interface,
// making K8s participate in the same pipeline worker as baremetal/docker.
package k8sdriver

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/piper/piper/internal/proto"
	k8smanifest "github.com/piper/piper/pkg/manifest/k8s"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
	"github.com/piper/piper/pkg/pipeline/worker/driver"
	k8slauncher "github.com/piper/piper/pkg/pipeline/worker/driver/k8slauncher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Config configures the K8sDriver.
type Config struct {
	WorkerID             string
	Namespaces           []string
	AgentImage           string
	AgentImagePullPolicy string
	TTLAfterFinished     *int32
	K8sClient            kubernetes.Interface
}

// Driver wraps k8s.Launcher to implement driver.Driver.
// It also implements driver.Observable — the Worker must call
// Observe(ctx) in a background goroutine to drive ReconcileJobs polling.
type Driver struct {
	cfg        Config
	launchers  map[string]*k8slauncher.Launcher // namespace → launcher
	launcherMu sync.Mutex

	mu             sync.Mutex
	waiters        map[string]chan driver.Exit // runtimeKey → signal channel
	namespaceByKey map[string]string
	// taskToKey maps task.ID → runtimeKey for reconcile callbacks.
	taskToKey map[string]string
}

// New creates a K8sDriver. K8sClient must be non-nil.
func New(cfg Config) (*Driver, error) {
	if cfg.K8sClient == nil {
		return nil, fmt.Errorf("k8s driver: K8sClient is required")
	}
	if cfg.TTLAfterFinished != nil && *cfg.TTLAfterFinished > 0 && *cfg.TTLAfterFinished < 30 {
		return nil, fmt.Errorf("k8s driver: TTLAfterFinished must be at least 30 seconds")
	}
	d := &Driver{
		cfg:            cfg,
		launchers:      make(map[string]*k8slauncher.Launcher),
		waiters:        make(map[string]chan driver.Exit),
		namespaceByKey: make(map[string]string),
		taskToKey:      make(map[string]string),
	}
	// Register launchers for every namespace that recovery must scan.
	namespaces := append([]string{}, cfg.Namespaces...)
	if len(namespaces) == 0 {
		namespaces = append(namespaces, "default")
	}
	for _, ns := range namespaces {
		d.launcher(ns)
	}
	return d, nil
}

// Start creates a K8s Job for the given task.
// ExecSpec provides environment-agnostic info; K8sDriver resolves container paths internally.
func (d *Driver) Start(ctx context.Context, task *proto.Task, spec driver.ExecSpec) (driver.Handle, error) {
	if task == nil {
		return driver.Handle{}, fmt.Errorf("k8s driver: task is required")
	}
	if spec.RuntimeKey == "" {
		return driver.Handle{}, fmt.Errorf("k8s driver: runtime key is required")
	}
	// Image and namespace are pre-resolved by the K8s worker; drivers must not
	// re-derive them from the task payload.
	image := spec.Image
	if image == "" {
		return driver.Handle{}, fmt.Errorf("k8s driver: spec.Image is required (resolve image before calling Start)")
	}
	namespace := spec.Namespace
	if namespace == "" {
		return driver.Handle{}, fmt.Errorf("k8s driver: spec.Namespace is required")
	}
	agentArgs, err := agent.BuildAgentExec(task, agent.AgentExecConfig{
		StorageToken: spec.StorageToken,
		StorageURL:   spec.StorageURL,
		OutputDir:    "/piper-outputs",
		InputDir:     "/piper-inputs",
		ResultFile:   "/dev/termination-log",
	})
	if err != nil {
		return driver.Handle{}, fmt.Errorf("k8s driver: build agent args: %w", err)
	}
	launcher := d.launcher(namespace)
	runtimeKey, err := launcher.CreateJob(ctx, task, spec.RuntimeKey, image, agentArgs, spec.Env)
	if err != nil {
		return driver.Handle{}, fmt.Errorf("k8s dispatch: %w", err)
	}

	handle := driver.Handle{
		RuntimeKey: runtimeKey,
		WorkerID:   d.cfg.WorkerID,
		TaskID:     task.ID,
		RunID:      task.RunID,
		StepName:   task.StepName,
		Attempt:    max(task.Attempt, 1),
		// ResultPath is empty for K8s; result comes from termination log via K8s API.
	}

	ch := make(chan driver.Exit, 1)
	d.mu.Lock()
	d.waiters[runtimeKey] = ch
	d.taskToKey[task.ID] = runtimeKey
	d.namespaceByKey[runtimeKey] = namespace
	d.mu.Unlock()

	return handle, nil
}

// Wait blocks until the K8s Job completes or ctx is cancelled.
// Completion is signaled by Observe()'s reconcile loop.
func (d *Driver) Wait(ctx context.Context, handle driver.Handle) (driver.Exit, error) {
	d.mu.Lock()
	ch := d.waiters[handle.RuntimeKey]
	d.mu.Unlock()

	if ch == nil {
		return driver.Exit{InfraFailure: fmt.Errorf("k8s job %q not tracked", handle.RuntimeKey)}, nil
	}

	select {
	case exit := <-ch:
		d.forget(handle)
		return exit, nil
	case <-ctx.Done():
		d.forget(handle)
		return driver.Exit{}, ctx.Err()
	}
}

// Stop deletes the K8s Job identified by the handle.
func (d *Driver) Stop(ctx context.Context, handle driver.Handle, _ time.Duration) error {
	d.mu.Lock()
	namespace := d.namespaceByKey[handle.RuntimeKey]
	d.mu.Unlock()
	if namespace == "" {
		return fmt.Errorf("k8s driver: namespace is required for stop")
	}
	err := d.launcher(namespace).DeleteJob(ctx, handle.RuntimeKey)
	d.forget(handle)
	return err
}

// StopRun deletes all Jobs for a run, including Jobs not yet recovered in memory.
func (d *Driver) StopRun(ctx context.Context, runID, namespace string) error {
	if runID == "" {
		return fmt.Errorf("k8s driver: run ID is required")
	}
	if namespace == "" {
		return fmt.Errorf("k8s driver: namespace is required for run cancellation")
	}
	if len(d.cfg.Namespaces) > 0 && !slices.Contains(d.cfg.Namespaces, namespace) {
		return fmt.Errorf("k8s driver: namespace %q is not allowed", namespace)
	}
	return d.launcher(namespace).CancelRun(ctx, runID)
}

// Recover rebuilds the in-memory watch set from K8s Job annotations after restart.
func (d *Driver) Recover(ctx context.Context) ([]driver.Handle, error) {
	if len(d.cfg.Namespaces) == 0 {
		selector := k8smanifest.ManagedSelector()
		if d.cfg.WorkerID != "" {
			selector = k8smanifest.WorkerSelector(d.cfg.WorkerID)
		}
		jobs, err := d.cfg.K8sClient.BatchV1().Jobs("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("k8s driver: discover recovery namespaces: %w", err)
		}
		for i := range jobs.Items {
			d.launcher(jobs.Items[i].Namespace)
		}
	}

	var handles []driver.Handle
	for namespace, launcher := range d.snapshotLaunchers() {
		launcher.RecoverJobs(ctx)
		for _, job := range launcher.ActiveJobs() {
			handle := driver.Handle{
				RuntimeKey: job.RuntimeKey,
				WorkerID:   d.cfg.WorkerID,
				TaskID:     job.TaskID,
				RunID:      job.RunID,
				StepName:   job.StepName,
				Attempt:    job.Attempt,
			}
			ch := make(chan driver.Exit, 1)
			d.mu.Lock()
			d.taskToKey[job.TaskID] = job.RuntimeKey
			d.waiters[job.RuntimeKey] = ch
			d.namespaceByKey[job.RuntimeKey] = namespace
			d.mu.Unlock()
			handles = append(handles, handle)
		}
	}
	if len(handles) > 0 {
		slog.Info("k8s driver: recovered jobs", "count", len(handles))
	}
	return handles, nil
}

// Observe implements driver.Observable. The Worker calls this in a
// background goroutine. It polls K8s Job status every 10 seconds and signals
// any pending Wait() calls when a job reaches a terminal state.
func (d *Driver) Observe(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		d.reconcileOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Driver) reconcileOnce(ctx context.Context) {
	for _, launcher := range d.snapshotLaunchers() {
		launcher.ReconcileJobs(ctx, func(_ context.Context, result proto.TaskResult) error {
			d.mu.Lock()
			runtimeKey, ok := d.taskToKey[result.TaskID]
			if !ok {
				runtimeKey = result.TaskID
			}
			ch := d.waiters[runtimeKey]
			d.mu.Unlock()

			if ch != nil {
				exit := driver.Exit{Result: &result}
				select {
				case ch <- exit:
				default:
				}
			}
			return nil
		})
	}
}

// ── namespace / launcher helpers ─────────────────────────────────────────────

func (d *Driver) launcher(namespace string) *k8slauncher.Launcher {
	d.launcherMu.Lock()
	defer d.launcherMu.Unlock()
	if l, ok := d.launchers[namespace]; ok {
		return l
	}
	l := k8slauncher.NewWithClient(k8slauncher.Config{
		AgentImage:           d.cfg.AgentImage,
		Namespace:            namespace,
		AgentImagePullPolicy: d.cfg.AgentImagePullPolicy,
		TTLAfterFinished:     d.cfg.TTLAfterFinished,
		WorkerID:             d.cfg.WorkerID,
	}, d.cfg.K8sClient)
	d.launchers[namespace] = l
	return l
}

func (d *Driver) snapshotLaunchers() map[string]*k8slauncher.Launcher {
	d.launcherMu.Lock()
	defer d.launcherMu.Unlock()
	out := make(map[string]*k8slauncher.Launcher, len(d.launchers))
	for namespace, launcher := range d.launchers {
		out[namespace] = launcher
	}
	return out
}

func (d *Driver) forget(handle driver.Handle) {
	d.mu.Lock()
	delete(d.waiters, handle.RuntimeKey)
	delete(d.taskToKey, handle.TaskID)
	delete(d.namespaceByKey, handle.RuntimeKey)
	d.mu.Unlock()
}

// Verify interface compliance at compile time.
var (
	_ driver.Driver     = (*Driver)(nil)
	_ driver.Observable = (*Driver)(nil)
	_ driver.RunStopper = (*Driver)(nil)
)
