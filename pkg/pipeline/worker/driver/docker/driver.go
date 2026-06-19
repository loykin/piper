// Package docker implements RuntimeDriver for Docker container execution.
// Each pipeline step runs as a one-shot container using piper agent exec.
package dockerdriver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	dockerclient "github.com/moby/moby/client"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
	"github.com/piper/piper/pkg/pipeline/worker/driver" //nolint:depguard
)

const (
	labelManaged    = "piper.managed"
	labelPipeline   = "piper.pipeline"
	labelWorkerID   = "piper.worker-id"
	labelRuntimeKey = "piper.runtime-key"
	labelTaskID     = "piper.task-id"
	labelRunID      = "piper.run-id"
	labelStepName   = "piper.step-name"
	labelAttempt    = "piper.attempt"
	labelResultPath = "piper.result-path"
)

// Config configures the DockerDriver.
type Config struct {
	WorkerID     string
	DefaultImage string // fallback image when step has none
	ResultDir    string // host directory for result files
	OutputDir    string // host output root directory
	// Network is the Docker network to attach containers to.
	Network string
}

// Driver is the Docker RuntimeDriver backed by the Docker daemon.
type Driver struct {
	cfg      Config
	piperBin string // path to the running piper binary on the host
	client   dockerclient.APIClient

	mu     sync.Mutex
	active map[string]string // runtimeKey → containerID
}

// New creates a DockerDriver connected to the local Docker daemon.
func New(cfg Config) (*Driver, error) {
	piperBin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("docker driver: resolve executable: %w", err)
	}
	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker driver: create client: %w", err)
	}
	if err := os.MkdirAll(cfg.ResultDir, 0755); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker driver: create result dir: %w", err)
	}
	return &Driver{
		cfg:      cfg,
		piperBin: piperBin,
		client:   cli,
		active:   make(map[string]string),
	}, nil
}

// NewWithClient creates a Driver with an injected Docker client. Intended for testing.
func NewWithClient(cfg Config, cli dockerclient.APIClient) *Driver {
	return &Driver{cfg: cfg, client: cli, active: make(map[string]string)}
}

// Close releases the Docker client resources held by the driver.
func (d *Driver) Close() error {
	return d.client.Close()
}

// Start creates and runs a container for the given pipeline step.
// spec.Image must be pre-resolved by the worker; Start returns an error if it is empty.
func (d *Driver) Start(ctx context.Context, task *proto.Task, spec driver.ExecSpec) (driver.Handle, error) {
	image := spec.Image
	if image == "" {
		return driver.Handle{}, fmt.Errorf("docker driver: spec.Image is required (resolve image before calling Start)")
	}

	// Docker uses container-side paths for agent exec; mounts host dirs at those paths.
	resultDir := d.cfg.ResultDir
	if resultDir == "" {
		resultDir = filepath.Join(spec.OutputDir, ".results")
	}
	if err := os.MkdirAll(resultDir, 0755); err != nil {
		return driver.Handle{}, fmt.Errorf("create result dir: %w", err)
	}
	hostResultPath := filepath.Join(resultDir, spec.RuntimeKey+".result.json")
	containerResultFile := driver.ContainerResultDir + "/" + spec.RuntimeKey + ".result.json"

	agentArgs, err := agent.BuildAgentExec(task, agent.AgentExecConfig{
		MasterURL:    spec.MasterURL,
		WorkerToken:  spec.WorkerToken,
		StorageToken: spec.StorageToken,
		StorageURL:   spec.StorageURL,
		OutputDir:    driver.ContainerOutputDir,
		InputDir:     driver.ContainerInputDir,
		ResultFile:   containerResultFile,
		ReportMode:   agent.ReportModeFile,
	})
	if err != nil {
		return driver.Handle{}, fmt.Errorf("build agent args: %w", err)
	}

	handle := driver.Handle{
		RuntimeKey: spec.RuntimeKey,
		WorkerID:   d.cfg.WorkerID,
		TaskID:     task.ID,
		RunID:      task.RunID,
		StepName:   task.StepName,
		Attempt:    task.Attempt,
		ResultPath: hostResultPath,
	}

	// Container command: piper agent exec with container-side paths.
	cmd := append([]string{driver.ContainerPiperBin}, agentArgs...)

	// Env: pass host env vars for Git credentials.
	env := append([]string{}, spec.Env...)

	mounts := []mount.Mount{
		// piper binary (read-only)
		{
			Type:     mount.TypeBind,
			Source:   d.piperBin,
			Target:   driver.ContainerPiperBin,
			ReadOnly: true,
		},
		// result directory
		{
			Type:   mount.TypeBind,
			Source: resultDir,
			Target: driver.ContainerResultDir,
		},
		// output artifacts
		{
			Type:   mount.TypeBind,
			Source: spec.OutputDir,
			Target: driver.ContainerOutputDir,
		},
	}

	labels := map[string]string{
		labelManaged:    "true",
		labelPipeline:   "true",
		labelWorkerID:   d.cfg.WorkerID,
		labelRuntimeKey: spec.RuntimeKey,
		labelTaskID:     task.ID,
		labelRunID:      task.RunID,
		labelStepName:   task.StepName,
		labelAttempt:    strconv.Itoa(task.Attempt),
		labelResultPath: hostResultPath,
	}

	networkMode := container.NetworkMode("bridge")
	if d.cfg.Network != "" {
		networkMode = container.NetworkMode(d.cfg.Network)
	}

	resp, err := d.client.ContainerCreate(ctx, dockerclient.ContainerCreateOptions{
		Config: &container.Config{
			Image:  image,
			Cmd:    cmd,
			Env:    env,
			Labels: labels,
		},
		HostConfig: &container.HostConfig{
			Mounts:      mounts,
			NetworkMode: networkMode,
			AutoRemove:  false, // manual remove after we read the result
		},
		Name: spec.RuntimeKey,
	})
	if err != nil {
		return driver.Handle{}, fmt.Errorf("container create: %w", err)
	}

	if _, err := d.client.ContainerStart(ctx, resp.ID, dockerclient.ContainerStartOptions{}); err != nil {
		_, _ = d.client.ContainerRemove(ctx, resp.ID, dockerclient.ContainerRemoveOptions{Force: true})
		return driver.Handle{}, fmt.Errorf("container start: %w", err)
	}

	d.mu.Lock()
	d.active[spec.RuntimeKey] = resp.ID
	d.mu.Unlock()

	slog.Info("docker: container started", "runtime_key", spec.RuntimeKey, "container_id", resp.ID[:12], "image", image)
	return handle, nil
}

// Wait blocks until the container exits or ctx is cancelled.
func (d *Driver) Wait(ctx context.Context, handle driver.Handle) (driver.Exit, error) {
	d.mu.Lock()
	containerID := d.active[handle.RuntimeKey]
	d.mu.Unlock()

	if containerID == "" {
		return driver.Exit{InfraFailure: fmt.Errorf("container %q not tracked", handle.RuntimeKey)}, nil
	}

	waitResult := d.client.ContainerWait(ctx, containerID, dockerclient.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case <-ctx.Done():
		return driver.Exit{}, ctx.Err()
	case err := <-waitResult.Error:
		return driver.Exit{InfraFailure: fmt.Errorf("container wait: %w", err)}, nil
	case body := <-waitResult.Result:
		exit := driver.Exit{ResultPath: handle.ResultPath}
		if body.Error != nil && body.Error.Message != "" {
			exit.InfraFailure = fmt.Errorf("container exit: %s", body.Error.Message)
		} else if body.StatusCode != 0 {
			if _, err := os.Stat(handle.ResultPath); os.IsNotExist(err) {
				exit.InfraFailure = fmt.Errorf("container exited %d without result file", body.StatusCode)
			}
		}
		d.mu.Lock()
		delete(d.active, handle.RuntimeKey)
		d.mu.Unlock()
		_, _ = d.client.ContainerRemove(context.Background(), containerID, dockerclient.ContainerRemoveOptions{Force: true})
		return exit, nil
	}
}

// Stop stops and removes the container.
func (d *Driver) Stop(ctx context.Context, handle driver.Handle, grace time.Duration) error {
	d.mu.Lock()
	containerID := d.active[handle.RuntimeKey]
	delete(d.active, handle.RuntimeKey)
	d.mu.Unlock()

	if containerID == "" {
		return nil
	}
	secs := int(grace.Seconds())
	_, _ = d.client.ContainerStop(ctx, containerID, dockerclient.ContainerStopOptions{Timeout: &secs})
	_, _ = d.client.ContainerRemove(ctx, containerID, dockerclient.ContainerRemoveOptions{Force: true})
	return nil
}

// Recover re-attaches to running and exited containers. Exited containers are
// returned so Wait can immediately collect their result before removal.
func (d *Driver) Recover(ctx context.Context) ([]driver.Handle, error) {
	f := make(dockerclient.Filters).
		Add("label", labelManaged+"=true").
		Add("label", labelPipeline+"=true").
		Add("label", labelWorkerID+"="+d.cfg.WorkerID)
	listResult, err := d.client.ContainerList(ctx, dockerclient.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("docker recover: list containers: %w", err)
	}
	containers := listResult.Items

	var handles []driver.Handle
	for _, c := range containers {
		runtimeKey := c.Labels[labelRuntimeKey]
		if runtimeKey == "" {
			continue
		}
		attempt, _ := strconv.Atoi(c.Labels[labelAttempt])
		handle := driver.Handle{
			RuntimeKey: runtimeKey,
			WorkerID:   d.cfg.WorkerID,
			TaskID:     c.Labels[labelTaskID],
			RunID:      c.Labels[labelRunID],
			StepName:   c.Labels[labelStepName],
			Attempt:    attempt,
			ResultPath: c.Labels[labelResultPath],
		}
		d.mu.Lock()
		d.active[runtimeKey] = c.ID
		d.mu.Unlock()
		handles = append(handles, handle)
		slog.Info("docker: recovered container", "runtime_key", runtimeKey, "container_id", c.ID[:12])
	}
	return handles, nil
}
