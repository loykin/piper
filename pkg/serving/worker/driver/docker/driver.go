// Package docker implements the Docker-backed serving worker runtime.
// It uses the Docker API to deploy, stop, and observe model serving containers.
package docker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/moby/moby/api/types/container"
	network_ "github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"

	dockerinfra "github.com/piper/piper/internal/docker"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/serving/worker/driver"
)

const (
	dockerManagedLabel = "piper.managed"
	dockerServingLabel = "piper.serving"
	dockerProjectLabel = "piper.project-id"
	dockerRuntimeLabel = "piper.runtime-name"
	dockerWorkerLabel  = "piper.worker-id"
)

// Config holds the Docker serving runtime configuration.
type Config struct {
	WorkerID string
	Image    string // default image when spec does not specify one
	Network  string // docker network name (default: "bridge")
}

type containerState struct {
	id     string
	stopCh chan struct{} // closed when Stop is called intentionally
}

// Driver manages serving containers via the Docker API.
type Driver struct {
	workerID   string
	cfg        Config
	client     dockerinfra.API
	mu         sync.Mutex
	containers map[string]*containerState // runtimeName -> state
}

// New creates a Driver connected to the local Docker daemon.
func New(cfg Config) (*Driver, error) {
	if cfg.WorkerID == "" {
		return nil, fmt.Errorf("docker serving runtime requires a stable worker ID")
	}
	if cfg.Network == "" {
		cfg.Network = "bridge"
	}
	cli, err := dockerinfra.NewClient()
	if err != nil {
		return nil, err
	}
	return NewWithClient(cfg, cli)
}

func NewWithClient(cfg Config, cli dockerinfra.API) (*Driver, error) {
	if cfg.WorkerID == "" {
		return nil, fmt.Errorf("docker serving runtime requires a stable worker ID")
	}
	if cfg.Network == "" {
		cfg.Network = "bridge"
	}
	return &Driver{workerID: cfg.WorkerID, cfg: cfg, client: cli, containers: make(map[string]*containerState)}, nil
}

// Deploy starts a serving container and attaches an exit watcher. The caller
// must not call Deploy again for the same RuntimeName while the first container
// is still running. Health checks are owned by the Worker.
func (d *Driver) Deploy(ctx context.Context, req driver.DeployRequest) (endpoint string, err error) {
	image := req.Image
	if image == "" {
		image = d.cfg.Image
	}
	if image == "" {
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return "", fmt.Errorf("docker serving: image is required")
	}
	if req.Port == 0 {
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return "", fmt.Errorf("docker serving: port is required")
	}
	if len(req.Command) == 0 {
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return "", fmt.Errorf("docker serving: command is required")
	}

	// Remove any stale containers for this runtime name.
	if existing, listErr := d.findManagedContainers(ctx, req.RuntimeName); listErr == nil {
		for _, id := range existing {
			_, _ = d.client.ContainerStop(ctx, id, dockerclient.ContainerStopOptions{})
			_, _ = d.client.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true})
		}
	}

	envVars := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		envVars = append(envVars, k+"="+v)
	}

	portSpec := fmt.Sprintf("%d/tcp", req.Port)
	exposedPort := network_.MustParsePort(portSpec)
	labels := map[string]string{
		dockerManagedLabel: "true",
		dockerServingLabel: req.Name,
		dockerProjectLabel: req.ProjectID,
		dockerRuntimeLabel: req.RuntimeName,
		dockerWorkerLabel:  d.workerID,
	}

	created, createErr := d.client.ContainerCreate(ctx, dockerclient.ContainerCreateOptions{
		Name: containerName(req.RuntimeName),
		Config: &container.Config{
			Image:        image,
			Cmd:          req.Command[1:],
			Entrypoint:   []string{req.Command[0]},
			Env:          envVars,
			Labels:       labels,
			ExposedPorts: network_.PortSet{exposedPort: struct{}{}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:  container.NetworkMode(d.cfg.Network),
			PortBindings: network_.PortMap{exposedPort: []network_.PortBinding{{HostPort: fmt.Sprintf("%d", req.Port)}}},
		},
	})
	if createErr != nil {
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return "", fmt.Errorf("docker serving: create container: %w", createErr)
	}

	if _, startErr := d.client.ContainerStart(ctx, created.ID, dockerclient.ContainerStartOptions{}); startErr != nil {
		_, _ = d.client.ContainerRemove(context.Background(), created.ID, dockerclient.ContainerRemoveOptions{Force: true})
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return "", fmt.Errorf("docker serving: start container: %w", startErr)
	}

	state := &containerState{id: created.ID, stopCh: make(chan struct{})}
	d.mu.Lock()
	d.containers[req.RuntimeName] = state
	d.mu.Unlock()

	endpoint = fmt.Sprintf("http://localhost:%d", req.Port)

	if req.LogSink != nil {
		go dockerinfra.StreamLogs(d.client, created.ID, "svc:"+req.Name, "runtime", req.LogSink)
	}

	wait := d.client.ContainerWait(context.Background(), created.ID, dockerclient.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	go func() {
		status := serving.StatusStopped
		select {
		case waitErr := <-wait.Error:
			if waitErr != nil {
				select {
				case <-state.stopCh:
				default:
					status = serving.StatusFailed
				}
			}
		case result := <-wait.Result:
			select {
			case <-state.stopCh:
			default:
				if result.StatusCode != 0 {
					status = serving.StatusFailed
				}
			}
		}
		_, _ = d.client.ContainerRemove(context.Background(), created.ID, dockerclient.ContainerRemoveOptions{Force: true})
		d.mu.Lock()
		if cs, ok := d.containers[req.RuntimeName]; ok && cs.id == created.ID {
			delete(d.containers, req.RuntimeName)
		}
		d.mu.Unlock()
		if req.OnExit != nil {
			req.OnExit(status)
		}
	}()

	slog.Info("docker serving container started", "name", req.Name, "container_id", created.ID, "endpoint", endpoint)
	return endpoint, nil
}

// Stop stops and removes the serving container for the given runtime name.
func (d *Driver) Stop(ctx context.Context, runtimeName string) error {
	d.mu.Lock()
	state, ok := d.containers[runtimeName]
	if ok {
		close(state.stopCh)
		delete(d.containers, runtimeName)
	}
	d.mu.Unlock()

	var id string
	if ok {
		id = state.id
	} else {
		ids, listErr := d.findManagedContainers(ctx, runtimeName)
		if listErr != nil {
			return listErr
		}
		if len(ids) == 0 {
			return nil
		}
		id = ids[0]
	}
	timeout := 10
	if _, err := d.client.ContainerStop(ctx, id, dockerclient.ContainerStopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop serving container: %w", err)
	}
	return nil
}

// Status returns the current status of a serving container.
func (d *Driver) Status(ctx context.Context, runtimeName string) string {
	d.mu.Lock()
	_, tracked := d.containers[runtimeName]
	d.mu.Unlock()
	if tracked {
		return serving.StatusRunning
	}
	items, err := d.listManagedContainers(ctx, runtimeName)
	if err != nil || len(items) == 0 {
		return serving.StatusStopped
	}
	for _, item := range items {
		if item.State == container.StateRunning || item.State == container.StateRestarting {
			return serving.StatusRunning
		}
	}
	return serving.StatusStopped
}

// Recover scans for serving containers from a previous worker instance and
// re-attaches exit watchers. Running containers call onRecovered; dead ones call onTerminal.
func (d *Driver) Recover(
	ctx context.Context,
	onRecovered func(driver.RecoveredHandle) func(status string),
	onTerminal func(driver.RecoveredHandle, string),
) error {
	items, err := d.listManagedContainers(ctx, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		name := item.Labels[dockerServingLabel]
		rn := item.Labels[dockerRuntimeLabel]
		if name == "" || rn == "" {
			continue
		}
		if item.Labels[dockerWorkerLabel] != d.workerID {
			continue
		}
		rec := driver.RecoveredHandle{
			ProjectID:   item.Labels[dockerProjectLabel],
			Name:        name,
			RuntimeName: rn,
			Port:        dockerHostPort(item, 0),
		}
		id := item.ID
		if item.State == container.StateRunning || item.State == container.StateRestarting {
			state := &containerState{id: id, stopCh: make(chan struct{})}
			d.mu.Lock()
			d.containers[rn] = state
			d.mu.Unlock()
			onExit := onRecovered(rec)
			wait := d.client.ContainerWait(context.Background(), id, dockerclient.ContainerWaitOptions{
				Condition: container.WaitConditionNotRunning,
			})
			go func(rn, containerID string, state *containerState) {
				status := serving.StatusStopped
				select {
				case waitErr := <-wait.Error:
					if waitErr != nil {
						select {
						case <-state.stopCh:
						default:
							status = serving.StatusFailed
						}
					}
				case result := <-wait.Result:
					select {
					case <-state.stopCh:
					default:
						if result.StatusCode != 0 {
							status = serving.StatusFailed
						}
					}
				}
				_, _ = d.client.ContainerRemove(context.Background(), containerID, dockerclient.ContainerRemoveOptions{Force: true})
				d.mu.Lock()
				if cs, ok := d.containers[rn]; ok && cs.id == containerID {
					delete(d.containers, rn)
				}
				d.mu.Unlock()
				onExit(status)
			}(rn, id, state)
		} else {
			status := serving.StatusStopped
			if result, inspectErr := d.client.ContainerInspect(ctx, id, dockerclient.ContainerInspectOptions{}); inspectErr == nil {
				if result.Container.State != nil && result.Container.State.ExitCode != 0 {
					status = serving.StatusFailed
				}
			}
			_, _ = d.client.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true})
			onTerminal(rec, status)
		}
	}
	return nil
}

// KillAll stops all tracked serving containers.
func (d *Driver) KillAll(ctx context.Context) error {
	ids, listErr := d.findManagedContainers(ctx, "")
	d.mu.Lock()
	for _, state := range d.containers {
		ids = append(ids, state.id)
	}
	d.containers = make(map[string]*containerState)
	d.mu.Unlock()

	seen := make(map[string]bool)
	timeout := 3
	var firstErr error
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := d.client.ContainerStop(ctx, id, dockerclient.ContainerStopOptions{Timeout: &timeout}); err != nil && firstErr == nil {
			firstErr = err
		}
		if _, err := d.client.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if listErr != nil {
		return listErr
	}
	return firstErr
}

func (d *Driver) listManagedContainers(ctx context.Context, runtimeName string) ([]container.Summary, error) {
	filters := dockerclient.Filters{}.Add("label", dockerManagedLabel+"=true")
	filters = filters.Add("label", dockerWorkerLabel+"="+d.workerID)
	if runtimeName != "" {
		filters = filters.Add("label", dockerRuntimeLabel+"="+runtimeName)
	}
	list, err := d.client.ContainerList(ctx, dockerclient.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list serving containers: %w", err)
	}
	return list.Items, nil
}

func (d *Driver) findManagedContainers(ctx context.Context, runtimeName string) ([]string, error) {
	items, err := d.listManagedContainers(ctx, runtimeName)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func dockerHostPort(item container.Summary, privatePort uint16) int {
	for _, port := range item.Ports {
		if (privatePort == 0 || port.PrivatePort == privatePort) && port.Type == "tcp" {
			return int(port.PublicPort)
		}
	}
	return 0
}

func containerName(rn string) string {
	clean := strings.NewReplacer(":", "-", "/", "-", "__", "-").Replace(rn)
	if len(clean) > 48 {
		clean = clean[:48]
	}
	return "piper-serving-" + clean
}
