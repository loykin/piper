package notebookworker

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/docker/go-units"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	network_ "github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"

	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/notebook"
)

const (
	dockerNotebookPort  = "8888/tcp"
	dockerManagedLabel  = "piper.managed"
	dockerNotebookLabel = "piper.notebook"
	dockerWorkerLabel   = "piper.worker-id"
)

type DockerConfig struct {
	Image        string
	Network      string
	CPUs         string
	Memory       string
	ShmSize      string
	ReadOnlyRoot bool
	Tmpfs        []string
	User         string
	Volumes      []DockerVolume
	ExtraArgs    []string
}

type DockerVolume struct {
	Name          string
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

type dockerAPI interface {
	ContainerCreate(ctx context.Context, options dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error)
	ContainerInspect(ctx context.Context, containerID string, options dockerclient.ContainerInspectOptions) (dockerclient.ContainerInspectResult, error)
	ContainerStart(ctx context.Context, containerID string, options dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error)
	ContainerStop(ctx context.Context, containerID string, options dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error)
	ContainerRemove(ctx context.Context, containerID string, options dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error)
	ContainerList(ctx context.Context, options dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error)
	ContainerWait(ctx context.Context, containerID string, options dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult
}

type dockerRuntime struct {
	workerID   string
	cfg        DockerConfig
	client     dockerAPI
	mu         sync.Mutex
	containers map[string]string // name -> containerID cache; Docker daemon is source of truth
}

func newDockerRuntime(cfg DockerConfig, workerID string) (*dockerRuntime, error) {
	if workerID == "" {
		return nil, fmt.Errorf("docker notebook runtime requires a stable worker ID")
	}
	normalized, err := normalizeDockerConfig(cfg)
	if err != nil {
		return nil, err
	}
	cli, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	return &dockerRuntime{
		workerID:   workerID,
		cfg:        normalized,
		client:     cli,
		containers: make(map[string]string),
	}, nil
}

func newDockerClient() (*dockerclient.Client, error) {
	if os.Getenv("DOCKER_HOST") != "" {
		return dockerclient.New(dockerclient.FromEnv)
	}
	if home, err := os.UserHomeDir(); err == nil {
		desktopSock := filepath.Join(home, ".docker", "run", "docker.sock")
		if info, statErr := os.Stat(desktopSock); statErr == nil && !info.IsDir() {
			return dockerclient.New(dockerclient.FromEnv, dockerclient.WithHost("unix://"+desktopSock))
		}
	}
	return dockerclient.New(dockerclient.FromEnv)
}

func newDockerRuntimeWithClient(cfg DockerConfig, workerID string, cli dockerAPI) (*dockerRuntime, error) {
	if workerID == "" {
		return nil, fmt.Errorf("docker notebook runtime requires a stable worker ID")
	}
	normalized, err := normalizeDockerConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &dockerRuntime{workerID: workerID, cfg: normalized, client: cli, containers: make(map[string]string)}, nil
}

func (r *dockerRuntime) Start(ctx context.Context, req RuntimeStartRequest) (*StartedNotebook, error) {
	options, err := r.containerCreateOptions(req)
	if err != nil {
		return nil, err
	}
	existing, err := r.findManagedContainer(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	for _, id := range existing {
		if err := r.removeContainer(ctx, id); err != nil {
			return nil, fmt.Errorf("remove existing notebook container %s: %w", id, err)
		}
	}
	created, err := r.client.ContainerCreate(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("create notebook container: %w", err)
	}
	if _, err := r.client.ContainerStart(ctx, created.ID, dockerclient.ContainerStartOptions{}); err != nil {
		_, _ = r.client.ContainerRemove(context.Background(), created.ID, dockerclient.ContainerRemoveOptions{Force: true})
		return nil, fmt.Errorf("start notebook container: %w", err)
	}

	r.mu.Lock()
	r.containers[req.Name] = created.ID
	r.mu.Unlock()

	wait := r.client.ContainerWait(context.Background(), created.ID, dockerclient.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	go func() {
		status := "stopped"
		select {
		case err := <-wait.Error:
			if err != nil {
				status = "failed"
			}
		case result := <-wait.Result:
			if result.StatusCode != 0 {
				status = "failed"
			}
		}
		_ = r.removeContainer(context.Background(), created.ID)
		r.mu.Lock()
		if r.containers[req.Name] == created.ID {
			delete(r.containers, req.Name)
		}
		r.mu.Unlock()
		if req.OnExit != nil {
			req.OnExit(status)
		}
	}()

	endpoint := fmt.Sprintf("http://localhost:%d", req.Port)
	slog.Info("notebook docker container started", "name", req.Name, "container_id", created.ID, "endpoint", endpoint)
	return &StartedNotebook{Endpoint: endpoint, ContainerID: created.ID}, nil
}

func (r *dockerRuntime) Stop(ctx context.Context, name string) error {
	r.mu.Lock()
	id, ok := r.containers[name]
	if ok {
		delete(r.containers, name)
	}
	r.mu.Unlock()
	if !ok {
		ids, err := r.findManagedContainer(ctx, name)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return fmt.Errorf("notebook not found")
		}
		id = ids[0]
	}
	timeout := 10
	if _, err := r.client.ContainerStop(ctx, id, dockerclient.ContainerStopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop notebook container: %w", err)
	}
	return nil
}

func (r *dockerRuntime) KillAll(ctx context.Context) error {
	ids, err := r.findManagedContainer(ctx, "")
	if err != nil {
		return err
	}
	r.mu.Lock()
	for _, id := range r.containers {
		ids = append(ids, id)
	}
	r.containers = make(map[string]string)
	r.mu.Unlock()

	seen := make(map[string]bool, len(ids))
	timeout := 3
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		_, _ = r.client.ContainerStop(ctx, id, dockerclient.ContainerStopOptions{Timeout: &timeout})
		if err := r.removeContainer(ctx, id); err != nil {
			slog.Warn("remove notebook container failed", "container_id", id, "err", err)
		}
	}
	return nil
}

func (r *dockerRuntime) Status(name string) string {
	items, err := r.listManagedContainers(context.Background(), name)
	if err != nil {
		r.mu.Lock()
		_, tracked := r.containers[name]
		r.mu.Unlock()
		if tracked {
			return notebook.StatusRunning
		}
		return notebook.StatusStopped
	}
	if len(items) == 0 {
		return notebook.StatusStopped
	}
	for _, item := range items {
		if item.State == container.StateRunning || item.State == container.StateRestarting {
			return notebook.StatusRunning
		}
	}
	return notebook.StatusStopped
}

// Recover reconnects to containers left running by a previous worker instance.
// onRecovered is called synchronously before the exit watcher is attached.
func (r *dockerRuntime) Recover(
	ctx context.Context,
	onRecovered func(recoveredRuntime) func(status string),
	onTerminal func(name, status string),
) error {
	items, err := r.listManagedContainers(ctx, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		name := item.Labels[dockerNotebookLabel]
		if name == "" {
			continue
		}
		// Docker applies the worker label filter server-side. Recheck ownership at
		// this boundary so a non-conforming client or daemon response cannot attach
		// another worker's container to this runtime.
		if item.Labels[dockerWorkerLabel] != r.workerID {
			continue
		}
		id := item.ID
		if item.State == container.StateRunning || item.State == container.StateRestarting {
			r.mu.Lock()
			r.containers[name] = id
			r.mu.Unlock()
			onExit := onRecovered(recoveredRuntime{Name: name, Port: dockerHostPort(item)})
			wait := r.client.ContainerWait(context.Background(), id, dockerclient.ContainerWaitOptions{
				Condition: container.WaitConditionNotRunning,
			})
			go func(nbName, containerID string) {
				status := notebook.StatusStopped
				select {
				case err := <-wait.Error:
					if err != nil {
						status = notebook.StatusFailed
					}
				case result := <-wait.Result:
					if result.StatusCode != 0 {
						status = notebook.StatusFailed
					}
				}
				_ = r.removeContainer(context.Background(), containerID)
				r.mu.Lock()
				if r.containers[nbName] == containerID {
					delete(r.containers, nbName)
				}
				r.mu.Unlock()
				onExit(status)
			}(name, id)
		} else {
			status := r.containerExitStatus(ctx, id)
			if err := r.removeContainer(ctx, id); err != nil {
				return fmt.Errorf("remove terminal notebook container %s: %w", id, err)
			}
			onTerminal(name, status)
		}
	}
	return nil
}

func (r *dockerRuntime) containerExitStatus(ctx context.Context, id string) string {
	result, err := r.client.ContainerInspect(ctx, id, dockerclient.ContainerInspectOptions{})
	if err == nil && result.Container.State != nil && result.Container.State.ExitCode == 0 {
		return notebook.StatusStopped
	}
	return notebook.StatusFailed
}

func dockerHostPort(item container.Summary) int {
	for _, port := range item.Ports {
		if port.PrivatePort == 8888 && port.Type == "tcp" {
			return int(port.PublicPort)
		}
	}
	return 0
}

func (r *dockerRuntime) containerCreateOptions(req RuntimeStartRequest) (dockerclient.ContainerCreateOptions, error) {
	ds := req.Spec.Spec.Driver.Docker // per-notebook overrides (may be nil)

	image := r.cfg.Image
	if ds != nil && ds.Image != "" {
		image = ds.Image
	}
	if image == "" {
		return dockerclient.ContainerCreateOptions{}, fmt.Errorf("docker image is required")
	}
	containerName := dockerContainerName(req.Name)

	// Merge tmpfs: server-level defaults, overridden by per-notebook spec.
	tmpfsPaths := r.cfg.Tmpfs
	if ds != nil && len(ds.Tmpfs) > 0 {
		tmpfsPaths = ds.Tmpfs
	}
	tmpfs := make(map[string]string, len(tmpfsPaths))
	for _, path := range tmpfsPaths {
		tmpfs[path] = ""
	}

	resources, err := dockerResources(r.cfg, ds)
	if err != nil {
		return dockerclient.ContainerCreateOptions{}, err
	}

	var volumeNames []string
	if ds != nil {
		volumeNames = ds.Volumes
	}
	selected, err := selectDockerVolumes(r.cfg.Volumes, volumeNames)
	if err != nil {
		return dockerclient.ContainerCreateOptions{}, err
	}
	mounts, err := dockerMounts(req.WorkDir, selected)
	if err != nil {
		return dockerclient.ContainerCreateOptions{}, err
	}

	// Per-notebook overrides take precedence over server-level config defaults.
	user := r.cfg.User
	if ds != nil && ds.User != "" {
		user = ds.User
	}
	network := r.cfg.Network
	if ds != nil && ds.NetworkMode != "" {
		network = ds.NetworkMode
	}
	readOnly := r.cfg.ReadOnlyRoot
	if ds != nil && ds.ReadOnly {
		readOnly = ds.ReadOnly
	}

	labels := map[string]string{
		dockerManagedLabel:  "true",
		dockerNotebookLabel: req.Name,
		dockerWorkerLabel:   r.workerID,
	}
	prepSteps, err := prepareStepsForBackend(req.Spec.Spec.Prepare, notebook.PrepareBackendDocker)
	if err != nil {
		return dockerclient.ContainerCreateOptions{}, err
	}
	cmd := notebook.JupyterStartArgs(req.BaseURL, req.Token, notebook.ContainerWorkDir, 8888)
	cmd = append(cmd, r.cfg.ExtraArgs...)
	entrypoint := []string(nil)
	if len(prepSteps) > 0 {
		script, err := notebook.BuildLaunchScript(nil, prepSteps, cmd, notebook.ContainerWorkDir)
		if err != nil {
			return dockerclient.ContainerCreateOptions{}, err
		}
		entrypoint = []string{"/bin/sh"}
		cmd = []string{"-lc", script}
	}
	port := network_.MustParsePort(dockerNotebookPort)
	return dockerclient.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Entrypoint:   entrypoint,
			Image:        image,
			Cmd:          cmd,
			User:         user,
			WorkingDir:   notebook.ContainerWorkDir,
			Labels:       labels,
			ExposedPorts: network_.PortSet{port: struct{}{}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:    container.NetworkMode(network),
			PortBindings:   network_.PortMap{port: []network_.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(req.Port)}}},
			ReadonlyRootfs: readOnly,
			Tmpfs:          tmpfs,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			ShmSize:        resources.shmSize,
			Resources:      resources.resources,
			Mounts:         mounts,
		},
	}, nil
}

func (r *dockerRuntime) removeContainer(ctx context.Context, id string) error {
	_, err := r.client.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true})
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "is already in progress") || strings.Contains(msg, "No such container") {
		return nil
	}
	return err
}

func (r *dockerRuntime) listManagedContainers(ctx context.Context, name string) ([]container.Summary, error) {
	filters := dockerclient.Filters{}.Add("label", dockerManagedLabel+"=true")
	if name != "" {
		filters = filters.Add("label", dockerNotebookLabel+"="+name)
	}
	filters = filters.Add("label", dockerWorkerLabel+"="+r.workerID)
	list, err := r.client.ContainerList(ctx, dockerclient.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list notebook containers: %w", err)
	}
	return list.Items, nil
}

func (r *dockerRuntime) findManagedContainer(ctx context.Context, name string) ([]string, error) {
	items, err := r.listManagedContainers(ctx, name)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

type dockerResourceSpec struct {
	resources container.Resources
	shmSize   int64
}

// dockerResources merges server-level DockerConfig with per-notebook DriverDockerSpec.
// Per-notebook values override server-level defaults when set.
func dockerResources(cfg DockerConfig, ds *manifest.DriverDockerSpec) (dockerResourceSpec, error) {
	var out dockerResourceSpec

	// Memory: per-notebook mem_limit overrides server-level memory.
	memory := cfg.Memory
	if ds != nil && ds.MemLimit != "" {
		memory = ds.MemLimit
	}
	if memory != "" {
		n, err := units.RAMInBytes(memory)
		if err != nil {
			return out, fmt.Errorf("invalid docker memory %q: %w", memory, err)
		}
		out.resources.Memory = n
	}

	// ShmSize: per-notebook overrides server-level.
	shmSize := cfg.ShmSize
	if ds != nil && ds.ShmSize != "" {
		shmSize = ds.ShmSize
	}
	if shmSize != "" {
		n, err := units.RAMInBytes(shmSize)
		if err != nil {
			return out, fmt.Errorf("invalid docker shm_size %q: %w", shmSize, err)
		}
		out.shmSize = n
	}

	// CPUs: per-notebook overrides server-level.
	cpuStr := cfg.CPUs
	if ds != nil && ds.CPUs != "" {
		cpuStr = ds.CPUs
	}
	if cpuStr != "" {
		cpus, err := strconv.ParseFloat(cpuStr, 64)
		if err != nil || cpus <= 0 {
			return out, fmt.Errorf("invalid docker cpus %q", cpuStr)
		}
		out.resources.NanoCPUs = int64(cpus * 1_000_000_000)
	}

	// GPU: extracted from per-notebook Docker Compose deploy.resources spec.
	if ds != nil && ds.Deploy != nil && ds.Deploy.Resources.Reservations != nil {
		for _, dev := range ds.Deploy.Resources.Reservations.Devices {
			isGPU := false
			for _, capability := range dev.Capabilities {
				if capability == "gpu" {
					isGPU = true
					break
				}
			}
			if !isGPU {
				continue
			}
			driver := dev.Driver
			if driver == "" {
				driver = "nvidia"
			}
			if len(dev.DeviceIDs) > 0 {
				out.resources.DeviceRequests = append(out.resources.DeviceRequests, container.DeviceRequest{
					Driver:       driver,
					DeviceIDs:    dev.DeviceIDs,
					Capabilities: [][]string{{"gpu"}},
				})
			} else {
				count := -1 // "all"
				if dev.Count != "" && dev.Count != "all" {
					n, err := strconv.Atoi(dev.Count)
					if err != nil || n <= 0 {
						return out, fmt.Errorf("invalid docker device count %q", dev.Count)
					}
					count = n
				}
				out.resources.DeviceRequests = append(out.resources.DeviceRequests, container.DeviceRequest{
					Driver:       driver,
					Count:        count,
					Capabilities: [][]string{{"gpu"}},
				})
			}
		}
	}

	return out, nil
}

func dockerMounts(workDir string, vols []DockerVolume) ([]mount.Mount, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("invalid work_dir: %w", err)
	}
	mounts := []mount.Mount{{
		Type:   mount.TypeBind,
		Source: absWorkDir,
		Target: notebook.ContainerWorkDir,
	}}
	targets := map[string]bool{notebook.ContainerWorkDir: true}
	for _, vol := range vols {
		if targets[vol.ContainerPath] {
			return nil, fmt.Errorf("duplicate docker volume container_path %q", vol.ContainerPath)
		}
		targets[vol.ContainerPath] = true
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   vol.HostPath,
			Target:   vol.ContainerPath,
			ReadOnly: vol.ReadOnly,
		})
	}
	return mounts, nil
}

func selectDockerVolumes(allowlist []DockerVolume, names []string) ([]DockerVolume, error) {
	if len(names) == 0 {
		return nil, nil
	}
	byName := make(map[string]DockerVolume, len(allowlist))
	for _, vol := range allowlist {
		byName[vol.Name] = vol
	}
	out := make([]DockerVolume, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		vol, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("docker volume %q is not allowed", name)
		}
		out = append(out, vol)
	}
	return out, nil
}

func normalizeDockerConfig(cfg DockerConfig) (DockerConfig, error) {
	if cfg.Image == "" {
		cfg.Image = "jupyter/scipy-notebook:latest"
	}
	if cfg.Network == "" {
		cfg.Network = "bridge"
	}
	if cfg.Network != "bridge" && cfg.Network != "none" {
		return cfg, fmt.Errorf("notebook_worker.docker.network must be bridge or none")
	}
	seenNames := map[string]bool{}
	seenTargets := map[string]bool{notebook.ContainerWorkDir: true}
	for i, vol := range cfg.Volumes {
		if vol.Name == "" {
			return cfg, fmt.Errorf("notebook_worker.docker.volumes[%d].name is required", i)
		}
		if seenNames[vol.Name] {
			return cfg, fmt.Errorf("duplicate docker volume name %q", vol.Name)
		}
		seenNames[vol.Name] = true
		hostPath, err := secureHostPath(vol.HostPath)
		if err != nil {
			return cfg, fmt.Errorf("docker volume %q: %w", vol.Name, err)
		}
		if !filepath.IsAbs(vol.ContainerPath) {
			return cfg, fmt.Errorf("docker volume %q container_path must be absolute", vol.Name)
		}
		if vol.ContainerPath == notebook.ContainerWorkDir || strings.HasPrefix(vol.ContainerPath, notebook.ContainerWorkDir+"/") {
			return cfg, fmt.Errorf("docker volume %q overlaps notebook work dir", vol.Name)
		}
		if seenTargets[vol.ContainerPath] {
			return cfg, fmt.Errorf("duplicate docker volume container_path %q", vol.ContainerPath)
		}
		seenTargets[vol.ContainerPath] = true
		cfg.Volumes[i].HostPath = hostPath
		cfg.Volumes[i].ReadOnly = vol.ReadOnly
	}
	return cfg, nil
}

func secureHostPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("host_path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve host_path: %w", err)
	}
	if resolved == "/" {
		return "", fmt.Errorf("host root cannot be mounted")
	}
	home, _ := os.UserHomeDir()
	if home != "" && resolved == home {
		return "", fmt.Errorf("user home cannot be mounted wholesale")
	}
	if resolved == "/var/run/docker.sock" || resolved == "/run/docker.sock" {
		return "", fmt.Errorf("docker socket cannot be mounted")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat host_path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("host_path must be a directory")
	}
	return resolved, nil
}

var dockerNamePattern = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func dockerContainerName(name string) string {
	clean := strings.Trim(dockerNamePattern.ReplaceAllString(name, "-"), "-_.")
	if clean == "" {
		clean = "notebook"
	}
	if len(clean) > 48 {
		clean = clean[:48]
	}
	return "piper-notebook-" + clean
}
