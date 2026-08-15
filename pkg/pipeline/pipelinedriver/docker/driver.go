// Package docker implements RuntimeDriver for Docker container execution.
// Each pipeline step runs as a one-shot container using piper agent exec.
package dockerdriver

import (
	"context"
	"debug/elf"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	dockerclient "github.com/moby/moby/client"

	dockerinfra "github.com/loykin/piper/internal/docker"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/pipelinedriver" //nolint:depguard
	"github.com/loykin/piper/pkg/pipeline/worker/agent"
)

const (
	labelManaged    = "piper.managed"
	labelPipeline   = "piper.pipeline"
	labelRuntimeID  = "piper.runtime-id"
	labelRuntimeKey = "piper.runtime-key"
	labelTaskID     = "piper.task-id"
	labelRunID      = "piper.run-id"
	labelStepName   = "piper.step-name"
	labelAttempt    = "piper.attempt"
	labelResultPath = "piper.result-path"
	labelTaskPath   = "piper.task-path"
)

// Config configures the DockerDriver.
type Config struct {
	RuntimeID string
	ResultDir string // host directory for result files
	OutputDir string // host output root directory
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
	piperBin, err := resolvePiperBinary()
	if err != nil {
		return nil, fmt.Errorf("docker driver: resolve Linux agent executable: %w", err)
	}
	cli, err := dockerinfra.NewClient()
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

func resolvePiperBinary() (string, error) {
	if configured := os.Getenv("PIPER_DOCKER_AGENT_BINARY"); configured != "" {
		return validateLinuxBinary(configured)
	}
	if runtime.GOOS == "linux" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		return validateLinuxBinary(executable)
	}
	executable, executableErr := os.Executable()
	candidates := []string{}
	if executableErr == nil {
		executableDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(executableDir, "piper-"+runtime.GOARCH),
			filepath.Join(executableDir, "piper-agent"),
		)
	}
	// Keep repository-relative candidates for `go run` and development builds,
	// whose executable lives in a temporary directory rather than ./bin.
	candidates = append(candidates,
		filepath.Join("bin", "piper-"+runtime.GOARCH),
		filepath.Join("bin", "piper-agent"),
	)
	for _, candidate := range candidates {
		if resolved, err := validateLinuxBinary(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("no Linux/%s companion binary found; run `make build-linux-native` or set PIPER_DOCKER_AGENT_BINARY", runtime.GOARCH)
}

func validateLinuxBinary(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	file, err := elf.Open(abs)
	if err != nil {
		return "", fmt.Errorf("%s is not a Linux ELF executable: %w", abs, err)
	}
	defer file.Close()
	var want elf.Machine
	switch runtime.GOARCH {
	case "amd64":
		want = elf.EM_X86_64
	case "arm64":
		want = elf.EM_AARCH64
	default:
		return "", fmt.Errorf("Docker runtime is unsupported on host architecture %s", runtime.GOARCH)
	}
	if file.Machine != want {
		return "", fmt.Errorf("%s has architecture %s, want %s", abs, file.Machine, want)
	}
	return abs, nil
}

// NewWithClient creates a Driver with an injected Docker client. Intended for testing.
func NewWithClient(cfg Config, cli dockerclient.APIClient) *Driver {
	return &Driver{cfg: cfg, client: cli, active: make(map[string]string)}
}

// Close releases the Docker client resources held by the pipelinedriver.
func (d *Driver) Close() error {
	return d.client.Close()
}

// Start creates and runs a container for the given pipeline step.
// spec.Image must be pre-resolved by the caller; Start returns an error if it is empty.
func (d *Driver) Start(ctx context.Context, task *proto.Task, spec pipelinedriver.ExecSpec) (pipelinedriver.Handle, error) {
	image := spec.Image
	if image == "" {
		return pipelinedriver.Handle{}, fmt.Errorf("docker driver: spec.Image is required (resolve image before calling Start)")
	}

	// Docker uses container-side paths for agent exec; mounts host dirs at those paths.
	resultDir := d.cfg.ResultDir
	if resultDir == "" {
		resultDir = filepath.Join(spec.OutputDir, ".results")
	}
	if err := os.MkdirAll(resultDir, 0755); err != nil {
		return pipelinedriver.Handle{}, fmt.Errorf("create result dir: %w", err)
	}
	hostResultPath := filepath.Join(resultDir, spec.RuntimeKey+".result.json")
	containerResultFile := pipelinedriver.ContainerResultDir + "/" + spec.RuntimeKey + ".result.json"
	hostTaskPath := filepath.Join(resultDir, spec.RuntimeKey+".task.json")
	containerTaskFile := pipelinedriver.ContainerResultDir + "/" + spec.RuntimeKey + ".task.json"
	if err := agent.WriteTaskFile(hostTaskPath, task); err != nil {
		return pipelinedriver.Handle{}, fmt.Errorf("write task file: %w", err)
	}

	agentArgs, err := agent.BuildAgentExec(task, agent.AgentExecConfig{
		StorageToken: spec.StorageToken,
		StorageURL:   spec.StorageURL,
		OutputDir:    pipelinedriver.ContainerOutputDir,
		InputDir:     pipelinedriver.ContainerInputDir,
		TaskFile:     containerTaskFile,
		ResultFile:   containerResultFile,
	})
	if err != nil {
		_ = os.Remove(hostTaskPath)
		return pipelinedriver.Handle{}, fmt.Errorf("build agent args: %w", err)
	}

	handle := pipelinedriver.Handle{
		RuntimeKey: spec.RuntimeKey,
		RuntimeID:  d.cfg.RuntimeID,
		TaskID:     task.ID,
		RunID:      task.RunID,
		StepName:   task.StepName,
		Attempt:    task.Attempt,
		ResultPath: hostResultPath,
		TaskPath:   hostTaskPath,
	}

	// Container command: piper agent exec with container-side paths.
	cmd := append([]string{pipelinedriver.ContainerPiperBin}, agentArgs...)

	// Do not pass resolved task env here: Docker exposes container env through
	// inspect. piper agent exec reads task env from the mounted task file.
	env := []string(nil)

	mounts := []mount.Mount{
		// piper binary (read-only)
		{
			Type:     mount.TypeBind,
			Source:   d.piperBin,
			Target:   pipelinedriver.ContainerPiperBin,
			ReadOnly: true,
		},
		// result directory
		{
			Type:   mount.TypeBind,
			Source: resultDir,
			Target: pipelinedriver.ContainerResultDir,
		},
		// output artifacts
		{
			Type:   mount.TypeBind,
			Source: spec.OutputDir,
			Target: pipelinedriver.ContainerOutputDir,
		},
	}

	labels := map[string]string{
		labelManaged:    "true",
		labelPipeline:   "true",
		labelRuntimeID:  d.cfg.RuntimeID,
		labelRuntimeKey: spec.RuntimeKey,
		labelTaskID:     task.ID,
		labelRunID:      task.RunID,
		labelStepName:   task.StepName,
		labelAttempt:    strconv.Itoa(task.Attempt),
		labelResultPath: hostResultPath,
		labelTaskPath:   hostTaskPath,
	}

	networkMode := container.NetworkMode("bridge")
	if d.cfg.Network != "" {
		networkMode = container.NetworkMode(d.cfg.Network)
	}
	var step pipeline.Step
	if len(task.Step) > 0 {
		if err := json.Unmarshal(task.Step, &step); err != nil {
			_ = os.Remove(hostTaskPath)
			return pipelinedriver.Handle{}, fmt.Errorf("parse step: %w", err)
		}
	}
	resources, err := dockerinfra.ResourcesFromDriverDocker(step.Driver.Docker)
	if err != nil {
		_ = os.Remove(hostTaskPath)
		return pipelinedriver.Handle{}, err
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
			Resources:   resources.Resources,
			ShmSize:     resources.ShmSize,
		},
		Name: spec.RuntimeKey,
	})
	if err != nil {
		_ = os.Remove(hostTaskPath)
		return pipelinedriver.Handle{}, fmt.Errorf("container create: %w", err)
	}

	if _, err := d.client.ContainerStart(ctx, resp.ID, dockerclient.ContainerStartOptions{}); err != nil {
		_, _ = d.client.ContainerRemove(ctx, resp.ID, dockerclient.ContainerRemoveOptions{Force: true})
		_ = os.Remove(hostTaskPath)
		return pipelinedriver.Handle{}, fmt.Errorf("container start: %w", err)
	}
	if spec.LogSink != nil {
		go dockerinfra.StreamLogs(d.client, resp.ID, task.RunID, task.StepName, spec.LogSink)
	}

	d.mu.Lock()
	d.active[spec.RuntimeKey] = resp.ID
	d.mu.Unlock()

	slog.Info("docker: container started", "runtime_key", spec.RuntimeKey, "container_id", resp.ID[:12], "image", image)
	return handle, nil
}

// Wait blocks until the container exits or ctx is cancelled.
func (d *Driver) Wait(ctx context.Context, handle pipelinedriver.Handle) (pipelinedriver.Exit, error) {
	d.mu.Lock()
	containerID := d.active[handle.RuntimeKey]
	d.mu.Unlock()

	if containerID == "" {
		return pipelinedriver.Exit{InfraFailure: fmt.Errorf("container %q not tracked", handle.RuntimeKey)}, nil
	}

	waitResult := d.client.ContainerWait(ctx, containerID, dockerclient.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case <-ctx.Done():
		return pipelinedriver.Exit{}, ctx.Err()
	case err := <-waitResult.Error:
		d.cleanupContainer(containerID, handle)
		return pipelinedriver.Exit{InfraFailure: fmt.Errorf("container wait: %w", err)}, nil
	case body := <-waitResult.Result:
		exit := pipelinedriver.Exit{ResultPath: handle.ResultPath}
		if body.Error != nil && body.Error.Message != "" {
			exit.InfraFailure = fmt.Errorf("container exit: %s", body.Error.Message)
		} else if body.StatusCode != 0 {
			if _, err := os.Stat(handle.ResultPath); os.IsNotExist(err) {
				exit.InfraFailure = fmt.Errorf("container exited %d without result file", body.StatusCode)
			}
		}
		d.cleanupContainer(containerID, handle)
		return exit, nil
	}
}

func (d *Driver) cleanupContainer(containerID string, handle pipelinedriver.Handle) {
	d.mu.Lock()
	delete(d.active, handle.RuntimeKey)
	d.mu.Unlock()
	if containerID != "" {
		_, _ = d.client.ContainerRemove(context.Background(), containerID, dockerclient.ContainerRemoveOptions{Force: true})
	}
	_ = os.Remove(handle.TaskPath)
}

// Stop stops and removes the container.
func (d *Driver) Stop(ctx context.Context, handle pipelinedriver.Handle, grace time.Duration) error {
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
	_ = os.Remove(handle.TaskPath)
	return nil
}

// Recover re-attaches to running and exited containers. Exited containers are
// returned so Wait can immediately collect their result before removal.
func (d *Driver) Recover(ctx context.Context) ([]pipelinedriver.Handle, error) {
	f := make(dockerclient.Filters).
		Add("label", labelManaged+"=true").
		Add("label", labelPipeline+"=true").
		Add("label", labelRuntimeID+"="+d.cfg.RuntimeID)
	listResult, err := d.client.ContainerList(ctx, dockerclient.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("docker recover: list containers: %w", err)
	}
	containers := listResult.Items

	var handles []pipelinedriver.Handle
	for _, c := range containers {
		runtimeKey := c.Labels[labelRuntimeKey]
		if runtimeKey == "" {
			continue
		}
		attempt, _ := strconv.Atoi(c.Labels[labelAttempt])
		handle := pipelinedriver.Handle{
			RuntimeKey: runtimeKey,
			RuntimeID:  d.cfg.RuntimeID,
			TaskID:     c.Labels[labelTaskID],
			RunID:      c.Labels[labelRunID],
			StepName:   c.Labels[labelStepName],
			Attempt:    attempt,
			ResultPath: c.Labels[labelResultPath],
			TaskPath:   c.Labels[labelTaskPath],
		}
		d.mu.Lock()
		d.active[runtimeKey] = c.ID
		d.mu.Unlock()
		handles = append(handles, handle)
		slog.Info("docker: recovered container", "runtime_key", runtimeKey, "container_id", c.ID[:12])
	}
	return handles, nil
}
