// Package docker implements RuntimeDriver for Docker container execution.
// Each pipeline step runs as a one-shot container using piper agent exec.
package dockerdriver

import (
	"archive/tar"
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
)

// Config configures the DockerDriver.
type Config struct {
	RuntimeID string
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

	// The task and result files live only inside the container: the task
	// carries resolved secrets, and a host directory would have to be writable
	// by whatever uid the step image runs as. Outputs go to the artifact store,
	// so no host directory is mounted either (as k8s uses emptyDir).
	agentArgs, err := agent.BuildAgentExec(task, agent.AgentExecConfig{
		StorageToken: spec.StorageToken,
		StorageURL:   spec.StorageURL,
		OutputDir:    pipelinedriver.ContainerOutputDir,
		InputDir:     pipelinedriver.ContainerInputDir,
		TaskFile:     containerTaskFile(spec.RuntimeKey),
		ResultFile:   containerResultFile(spec.RuntimeKey),
	})
	if err != nil {
		return pipelinedriver.Handle{}, fmt.Errorf("build agent args: %w", err)
	}

	handle := pipelinedriver.Handle{
		RuntimeKey: spec.RuntimeKey,
		RuntimeID:  d.cfg.RuntimeID,
		TaskID:     task.ID,
		RunID:      task.RunID,
		StepName:   task.StepName,
		Attempt:    task.Attempt,
	}

	// Container command: piper agent exec with container-side paths.
	cmd := append([]string{pipelinedriver.ContainerPiperBin}, agentArgs...)

	// Do not pass resolved task env here: Docker exposes container env through
	// inspect. piper agent exec reads task env from the copied-in task file.
	env := []string(nil)

	mounts := []mount.Mount{
		// piper binary (read-only)
		{
			Type:     mount.TypeBind,
			Source:   d.piperBin,
			Target:   pipelinedriver.ContainerPiperBin,
			ReadOnly: true,
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
	}

	networkMode := container.NetworkMode("bridge")
	if d.cfg.Network != "" {
		networkMode = container.NetworkMode(d.cfg.Network)
	}
	var step pipeline.Step
	if len(task.Step) > 0 {
		if err := json.Unmarshal(task.Step, &step); err != nil {
			return pipelinedriver.Handle{}, fmt.Errorf("parse step: %w", err)
		}
	}
	resources, err := dockerinfra.ResourcesFromDriverDocker(step.Driver.Docker)
	if err != nil {
		return pipelinedriver.Handle{}, err
	}

	if err := dockerinfra.EnsureImage(ctx, d.client, image); err != nil {
		return pipelinedriver.Handle{}, err
	}
	resp, err := d.client.ContainerCreate(ctx, dockerclient.ContainerCreateOptions{
		Config: &container.Config{
			Image: image,
			// Override the image's ENTRYPOINT (as the K8s launcher does with
			// Command): otherwise an image like `ENTRYPOINT ["/piper"]` receives
			// the agent path as its first argument and exits before running.
			Entrypoint: cmd[:1],
			Cmd:        cmd[1:],
			Env:        env,
			Labels:     labels,
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
		return pipelinedriver.Handle{}, fmt.Errorf("container create: %w", err)
	}

	if err := d.copyRuntimeFiles(ctx, resp.ID, spec.RuntimeKey, task); err != nil {
		_, _ = d.client.ContainerRemove(ctx, resp.ID, dockerclient.ContainerRemoveOptions{Force: true})
		return pipelinedriver.Handle{}, fmt.Errorf("copy task file into container: %w", err)
	}
	if _, err := d.client.ContainerStart(ctx, resp.ID, dockerclient.ContainerStartOptions{}); err != nil {
		_, _ = d.client.ContainerRemove(ctx, resp.ID, dockerclient.ContainerRemoveOptions{Force: true})
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
		var exit pipelinedriver.Exit
		if body.Error != nil && body.Error.Message != "" {
			exit.InfraFailure = fmt.Errorf("container exit: %s", body.Error.Message)
		} else if result, err := d.readResult(ctx, containerID, handle.RuntimeKey); err != nil {
			exit.InfraFailure = fmt.Errorf("container exited %d without result file: %w", body.StatusCode, err)
		} else {
			exit.Result = result
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
		}
		d.mu.Lock()
		d.active[runtimeKey] = c.ID
		d.mu.Unlock()
		handles = append(handles, handle)
		slog.Info("docker: recovered container", "runtime_key", runtimeKey, "container_id", c.ID[:12])
	}
	return handles, nil
}

func containerTaskFile(runtimeKey string) string {
	return pipelinedriver.ContainerResultDir + "/" + runtimeKey + ".task.json"
}

func containerResultFile(runtimeKey string) string {
	return pipelinedriver.ContainerResultDir + "/" + runtimeKey + ".result.json"
}

// copyRuntimeFiles copies the task file into the created container, along
// with the agent's runtime, output and input roots. CopyUIDGID chowns them to
// the container's user, so a non-root image can read its 0600 task file and
// create the roots it could not create under / itself.
func (d *Driver) copyRuntimeFiles(ctx context.Context, containerID, runtimeKey string, task *proto.Task) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	dirs := []struct {
		path string
		mode int64
	}{
		{pipelinedriver.ContainerResultDir, 0700},
		{pipelinedriver.ContainerOutputDir, 0755},
		{pipelinedriver.ContainerInputDir, 0755},
	}
	for _, dir := range dirs {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: strings.TrimPrefix(dir.path, "/") + "/", Mode: dir.mode, ModTime: now}); err != nil {
			return err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: strings.TrimPrefix(containerTaskFile(runtimeKey), "/"), Mode: 0600, Size: int64(len(data)), ModTime: now}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	_, err = d.client.CopyToContainer(ctx, containerID, dockerclient.CopyToContainerOptions{
		DestinationPath: "/",
		Content:         &buf,
		CopyUIDGID:      true,
	})
	return err
}

// readResult reads the agent's result file out of the exited container.
func (d *Driver) readResult(ctx context.Context, containerID, runtimeKey string) (*proto.TaskResult, error) {
	res, err := d.client.CopyFromContainer(ctx, containerID, dockerclient.CopyFromContainerOptions{SourcePath: containerResultFile(runtimeKey)})
	if err != nil {
		return nil, err
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("read result archive: %w", err)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("read result archive: %w", err)
	}
	result, err := agent.ReadAgentResult(data)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
