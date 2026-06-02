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
	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
)

const (
	dockerWorkDir       = "/home/jovyan/work"
	dockerNotebookPort  = "8888/tcp"
	dockerManagedLabel  = "piper.managed"
	dockerNotebookLabel = "piper.notebook"
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
	ContainerStart(ctx context.Context, containerID string, options dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error)
	ContainerStop(ctx context.Context, containerID string, options dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error)
	ContainerRemove(ctx context.Context, containerID string, options dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error)
	ContainerList(ctx context.Context, options dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error)
	ContainerWait(ctx context.Context, containerID string, options dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult
}

type dockerRuntime struct {
	cfg        DockerConfig
	client     dockerAPI
	mu         sync.Mutex
	containers map[string]string
}

func newDockerRuntime(cfg DockerConfig) (*dockerRuntime, error) {
	normalized, err := normalizeDockerConfig(cfg)
	if err != nil {
		return nil, err
	}
	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return nil, err
	}
	return &dockerRuntime{
		cfg:        normalized,
		client:     cli,
		containers: make(map[string]string),
	}, nil
}

func newDockerRuntimeWithClient(cfg DockerConfig, cli dockerAPI) (*dockerRuntime, error) {
	normalized, err := normalizeDockerConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &dockerRuntime{cfg: normalized, client: cli, containers: make(map[string]string)}, nil
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
		if _, err := r.client.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true}); err != nil {
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
		r.mu.Lock()
		delete(r.containers, req.Name)
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
	if _, err := r.client.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove notebook container: %w", err)
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
		if _, err := r.client.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true}); err != nil {
			slog.Warn("remove notebook container failed", "container_id", id, "err", err)
		}
	}
	return nil
}

func (r *dockerRuntime) containerCreateOptions(req RuntimeStartRequest) (dockerclient.ContainerCreateOptions, error) {
	image := req.Spec.Spec.Image
	if image == "" {
		image = r.cfg.Image
	}
	if image == "" {
		return dockerclient.ContainerCreateOptions{}, fmt.Errorf("docker image is required")
	}
	containerName := dockerContainerName(req.Name)
	tmpfs := make(map[string]string, len(r.cfg.Tmpfs))
	for _, path := range r.cfg.Tmpfs {
		tmpfs[path] = ""
	}

	resources, err := dockerResources(r.cfg, req.Spec.Spec.GPUs)
	if err != nil {
		return dockerclient.ContainerCreateOptions{}, err
	}
	selected, err := selectDockerVolumes(r.cfg.Volumes, req.Spec.Spec.Volumes)
	if err != nil {
		return dockerclient.ContainerCreateOptions{}, err
	}
	mounts, err := dockerMounts(req.WorkDir, selected)
	if err != nil {
		return dockerclient.ContainerCreateOptions{}, err
	}

	labels := map[string]string{
		dockerManagedLabel:  "true",
		dockerNotebookLabel: req.Name,
	}
	cmd := []string{
		"start-notebook.py",
		"--ServerApp.base_url=" + req.BaseURL,
		"--ServerApp.token=" + req.Token,
		"--ServerApp.root_dir=" + dockerWorkDir,
		"--ServerApp.allow_origin=*",
		"--no-browser",
		"--port=8888",
	}
	cmd = append(cmd, r.cfg.ExtraArgs...)
	port := network.MustParsePort(dockerNotebookPort)
	return dockerclient.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image:        image,
			Cmd:          cmd,
			User:         r.cfg.User,
			WorkingDir:   dockerWorkDir,
			Labels:       labels,
			ExposedPorts: network.PortSet{port: struct{}{}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:    container.NetworkMode(r.cfg.Network),
			PortBindings:   network.PortMap{port: []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(req.Port)}}},
			ReadonlyRootfs: r.cfg.ReadOnlyRoot,
			Tmpfs:          tmpfs,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			ShmSize:        resources.shmSize,
			Resources:      resources.resources,
			Mounts:         mounts,
		},
	}, nil
}

func (r *dockerRuntime) findManagedContainer(ctx context.Context, name string) ([]string, error) {
	filters := dockerclient.Filters{}.Add("label", dockerManagedLabel+"=true")
	if name != "" {
		filters = filters.Add("label", dockerNotebookLabel+"="+name)
	}
	list, err := r.client.ContainerList(ctx, dockerclient.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list notebook containers: %w", err)
	}
	ids := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

type dockerResourceSpec struct {
	resources container.Resources
	shmSize   int64
}

func dockerResources(cfg DockerConfig, gpus string) (dockerResourceSpec, error) {
	var out dockerResourceSpec
	if cfg.Memory != "" {
		n, err := units.RAMInBytes(cfg.Memory)
		if err != nil {
			return out, fmt.Errorf("invalid docker memory %q: %w", cfg.Memory, err)
		}
		out.resources.Memory = n
	}
	if cfg.ShmSize != "" {
		n, err := units.RAMInBytes(cfg.ShmSize)
		if err != nil {
			return out, fmt.Errorf("invalid docker shm_size %q: %w", cfg.ShmSize, err)
		}
		out.shmSize = n
	}
	if cfg.CPUs != "" {
		cpus, err := strconv.ParseFloat(cfg.CPUs, 64)
		if err != nil || cpus <= 0 {
			return out, fmt.Errorf("invalid docker cpus %q", cfg.CPUs)
		}
		out.resources.NanoCPUs = int64(cpus * 1_000_000_000)
	}
	switch strings.TrimSpace(gpus) {
	case "", "none":
	case "all":
		out.resources.DeviceRequests = []container.DeviceRequest{{
			Driver:       "nvidia",
			Count:        -1,
			Capabilities: [][]string{{"gpu"}},
		}}
	default:
		ids := splitCSV(gpus)
		if len(ids) == 0 {
			return out, fmt.Errorf("invalid gpu request %q", gpus)
		}
		out.resources.DeviceRequests = []container.DeviceRequest{{
			Driver:       "nvidia",
			DeviceIDs:    ids,
			Capabilities: [][]string{{"gpu"}},
		}}
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
		Target: dockerWorkDir,
	}}
	targets := map[string]bool{dockerWorkDir: true}
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
	seenTargets := map[string]bool{dockerWorkDir: true}
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
		if vol.ContainerPath == dockerWorkDir || strings.HasPrefix(vol.ContainerPath, dockerWorkDir+"/") {
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

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
