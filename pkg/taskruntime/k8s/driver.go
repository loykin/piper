// Package k8s implements RuntimeDriver for Kubernetes Job execution.
// It wraps pkg/k8s.Launcher and exposes the taskruntime.Driver interface,
// making K8s participate in the same pipeline worker as baremetal/docker.
package k8sdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/piper/piper/pkg/internal/k8smeta"
	k8slauncher "github.com/piper/piper/pkg/k8s"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/taskruntime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Config configures the K8sDriver.
type Config struct {
	WorkerID             string
	Namespace            string
	Namespaces           []string
	AgentImage           string
	AgentImagePullPolicy string
	DefaultImage         string
	TTLAfterFinished     *int32
	K8sClient            kubernetes.Interface
}

// Driver wraps k8s.Launcher to implement taskruntime.Driver.
// It also implements taskruntime.Observable — the Worker must call
// Observe(ctx) in a background goroutine to drive ReconcileJobs polling.
type Driver struct {
	cfg        Config
	launchers  map[string]*k8slauncher.Launcher // namespace → launcher
	launcherMu sync.Mutex

	mu             sync.Mutex
	waiters        map[string]chan taskruntime.Exit // runtimeKey → signal channel
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
		waiters:        make(map[string]chan taskruntime.Exit),
		namespaceByKey: make(map[string]string),
		taskToKey:      make(map[string]string),
	}
	// Register launchers for every namespace that recovery must scan.
	namespaces := append([]string{}, cfg.Namespaces...)
	if cfg.Namespace != "" && !slices.Contains(namespaces, cfg.Namespace) {
		namespaces = append(namespaces, cfg.Namespace)
	}
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
func (d *Driver) Start(ctx context.Context, task *proto.Task, spec taskruntime.ExecSpec) (taskruntime.Handle, error) {
	if task == nil {
		return taskruntime.Handle{}, fmt.Errorf("k8s driver: task is required")
	}
	if spec.RuntimeKey == "" {
		return taskruntime.Handle{}, fmt.Errorf("k8s driver: runtime key is required")
	}
	namespace, err := d.resolveNamespace(task)
	if err != nil {
		return taskruntime.Handle{}, err
	}
	image, err := resolveImage(task, d.cfg.DefaultImage)
	if err != nil {
		return taskruntime.Handle{}, err
	}
	agentArgs, err := taskruntime.BuildAgentExec(task, taskruntime.AgentExecConfig{
		MasterURL:  spec.MasterURL,
		Token:      spec.Token,
		StorageURL: spec.StorageURL,
		OutputDir:  "/piper-outputs",
		InputDir:   "/piper-inputs",
		ResultFile: "/dev/termination-log",
		ReportMode: taskruntime.ReportModeFile,
	})
	if err != nil {
		return taskruntime.Handle{}, fmt.Errorf("k8s driver: build agent args: %w", err)
	}
	launcher := d.launcher(namespace)
	runtimeKey, err := launcher.CreateJob(ctx, task, spec.RuntimeKey, image, agentArgs, spec.Env)
	if err != nil {
		return taskruntime.Handle{}, fmt.Errorf("k8s dispatch: %w", err)
	}

	handle := taskruntime.Handle{
		RuntimeKey: runtimeKey,
		WorkerID:   d.cfg.WorkerID,
		TaskID:     task.ID,
		RunID:      task.RunID,
		StepName:   task.StepName,
		Attempt:    max(task.Attempt, 1),
		// ResultPath is empty for K8s; result comes from termination log via K8s API.
	}

	ch := make(chan taskruntime.Exit, 1)
	d.mu.Lock()
	d.waiters[runtimeKey] = ch
	d.taskToKey[task.ID] = runtimeKey
	d.namespaceByKey[runtimeKey] = namespace
	d.mu.Unlock()

	return handle, nil
}

// Wait blocks until the K8s Job completes or ctx is cancelled.
// Completion is signaled by Observe()'s reconcile loop.
func (d *Driver) Wait(ctx context.Context, handle taskruntime.Handle) (taskruntime.Exit, error) {
	d.mu.Lock()
	ch := d.waiters[handle.RuntimeKey]
	d.mu.Unlock()

	if ch == nil {
		return taskruntime.Exit{InfraFailure: fmt.Errorf("k8s job %q not tracked", handle.RuntimeKey)}, nil
	}

	select {
	case exit := <-ch:
		d.forget(handle)
		return exit, nil
	case <-ctx.Done():
		d.forget(handle)
		return taskruntime.Exit{}, ctx.Err()
	}
}

// Stop deletes the K8s Job identified by the handle.
func (d *Driver) Stop(ctx context.Context, handle taskruntime.Handle, _ time.Duration) error {
	d.mu.Lock()
	namespace := d.namespaceByKey[handle.RuntimeKey]
	d.mu.Unlock()
	if namespace == "" {
		namespace = d.defaultNamespace()
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
		namespace = d.defaultNamespace()
	}
	if len(d.cfg.Namespaces) > 0 && namespace != d.cfg.Namespace && !slices.Contains(d.cfg.Namespaces, namespace) {
		return fmt.Errorf("k8s driver: namespace %q is not allowed", namespace)
	}
	return d.launcher(namespace).CancelRun(ctx, runID)
}

// Recover rebuilds the in-memory watch set from K8s Job annotations after restart.
func (d *Driver) Recover(ctx context.Context) ([]taskruntime.Handle, error) {
	if len(d.cfg.Namespaces) == 0 {
		selector := k8smeta.ManagedSelector()
		if d.cfg.WorkerID != "" {
			selector = k8smeta.WorkerSelector(d.cfg.WorkerID)
		}
		jobs, err := d.cfg.K8sClient.BatchV1().Jobs("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("k8s driver: discover recovery namespaces: %w", err)
		}
		for i := range jobs.Items {
			d.launcher(jobs.Items[i].Namespace)
		}
	}

	var handles []taskruntime.Handle
	for namespace, launcher := range d.snapshotLaunchers() {
		launcher.RecoverJobs(ctx)
		for _, job := range launcher.ActiveJobs() {
			handle := taskruntime.Handle{
				RuntimeKey: job.RuntimeKey,
				WorkerID:   d.cfg.WorkerID,
				TaskID:     job.TaskID,
				RunID:      job.RunID,
				StepName:   job.StepName,
				Attempt:    job.Attempt,
			}
			ch := make(chan taskruntime.Exit, 1)
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

// Observe implements taskruntime.Observable. The Worker calls this in a
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
				exit := taskruntime.Exit{Result: &result}
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

func (d *Driver) resolveNamespace(task *proto.Task) (string, error) {
	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return "", fmt.Errorf("k8s driver: unmarshal pipeline: %w", err)
	}
	namespace := pl.Spec.Placement.Namespace
	if namespace == "" {
		namespace = d.defaultNamespace()
	}
	if len(d.cfg.Namespaces) > 0 && namespace != d.cfg.Namespace && !slices.Contains(d.cfg.Namespaces, namespace) {
		return "", fmt.Errorf("k8s driver: namespace %q is not allowed", namespace)
	}
	return namespace, nil
}

func resolveImage(task *proto.Task, defaultImage string) (string, error) {
	var step pipeline.Step
	if err := json.Unmarshal(task.Step, &step); err != nil {
		return "", fmt.Errorf("k8s driver: unmarshal step: %w", err)
	}
	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return "", fmt.Errorf("k8s driver: unmarshal pipeline: %w", err)
	}
	for _, image := range []string{
		step.Runner.Image,
		step.Run.Image,
		pl.Spec.Defaults.Image,
		defaultImage,
	} {
		if image != "" {
			return image, nil
		}
	}
	return "", fmt.Errorf("step %q: no container image configured", step.Name)
}

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

func (d *Driver) defaultNamespace() string {
	if d.cfg.Namespace != "" {
		return d.cfg.Namespace
	}
	if len(d.cfg.Namespaces) > 0 && d.cfg.Namespaces[0] != "" {
		return d.cfg.Namespaces[0]
	}
	return "default"
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

func (d *Driver) forget(handle taskruntime.Handle) {
	d.mu.Lock()
	delete(d.waiters, handle.RuntimeKey)
	delete(d.taskToKey, handle.TaskID)
	delete(d.namespaceByKey, handle.RuntimeKey)
	d.mu.Unlock()
}

// Verify interface compliance at compile time.
var (
	_ taskruntime.Driver     = (*Driver)(nil)
	_ taskruntime.Observable = (*Driver)(nil)
	_ taskruntime.RunStopper = (*Driver)(nil)
)
