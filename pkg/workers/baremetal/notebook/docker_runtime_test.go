package notebookworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"

	"github.com/piper/piper/pkg/notebook"
)

func TestDockerRuntimeContainerCreateOptions(t *testing.T) {
	root := t.TempDir()
	datasets := filepath.Join(root, "datasets")
	if err := os.Mkdir(datasets, 0755); err != nil {
		t.Fatal(err)
	}
	rt, err := newDockerRuntimeWithClient(DockerConfig{
		Image:   "jupyter/scipy-notebook:latest",
		Network: "bridge",
		CPUs:    "2",
		Memory:  "4g",
		ShmSize: "1g",
		ExtraArgs: []string{
			"--ServerApp.disable_check_xsrf=true",
		},
		Volumes: []DockerVolume{{
			Name:          "datasets",
			HostPath:      datasets,
			ContainerPath: "/mnt/datasets",
			ReadOnly:      true,
		}},
	}, "test-agent", &fakeDockerClient{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	var spec notebook.NotebookServerSpec
	spec.Metadata.Name = "analysis"
	spec.Spec.Docker = &notebook.NotebookDockerSpec{
		Volumes: []string{"datasets"},
		Deploy: &notebook.DockerDeploySpec{
			Resources: notebook.DockerDeployResources{
				Reservations: notebook.DockerReservations{
					Devices: []notebook.DockerDeviceSpec{{
						Driver:       "nvidia",
						DeviceIDs:    []string{"0", "1"},
						Capabilities: []string{"gpu"},
					}},
				},
			},
		},
	}

	opts, err := rt.containerCreateOptions(RuntimeStartRequest{
		Name:    "analysis",
		Spec:    spec,
		WorkDir: t.TempDir(),
		Port:    18888,
		Token:   "",
		BaseURL: "/notebooks/analysis/proxy/",
	})
	if err != nil {
		t.Fatalf("container options: %v", err)
	}
	if opts.Config.Image != "jupyter/scipy-notebook:latest" {
		t.Fatalf("image = %q", opts.Config.Image)
	}
	if opts.Name != "piper-notebook-analysis" {
		t.Fatalf("name = %q", opts.Name)
	}
	if got := opts.HostConfig.PortBindings[network.MustParsePort("8888/tcp")][0].HostPort; got != "18888" {
		t.Fatalf("host port = %q", got)
	}
	if opts.HostConfig.Resources.NanoCPUs != 2_000_000_000 {
		t.Fatalf("NanoCPUs = %d", opts.HostConfig.Resources.NanoCPUs)
	}
	if len(opts.HostConfig.Resources.DeviceRequests) != 1 {
		t.Fatalf("DeviceRequests = %#v", opts.HostConfig.Resources.DeviceRequests)
	}
	if len(opts.HostConfig.Mounts) != 2 {
		t.Fatalf("mounts = %#v", opts.HostConfig.Mounts)
	}
	if !opts.HostConfig.Mounts[1].ReadOnly {
		t.Fatal("datasets mount should be read-only")
	}
	if got := opts.Config.Cmd[len(opts.Config.Cmd)-1]; got != "--ServerApp.disable_check_xsrf=true" {
		t.Fatalf("last command arg = %q", got)
	}
	wantArgs := notebook.JupyterStartArgs("/notebooks/analysis/proxy/", "", notebook.ContainerWorkDir, 8888)
	for i, want := range wantArgs {
		if opts.Config.Cmd[i] != want {
			t.Fatalf("cmd[%d] = %q, want %q", i, opts.Config.Cmd[i], want)
		}
	}
}

func TestDockerRuntimeRejectsUnallowedVolume(t *testing.T) {
	rt, err := newDockerRuntimeWithClient(DockerConfig{}, "test-agent", &fakeDockerClient{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	var spec notebook.NotebookServerSpec
	spec.Metadata.Name = "analysis"
	spec.Spec.Docker = &notebook.NotebookDockerSpec{
		Volumes: []string{"host-root"},
	}

	if _, err := rt.containerCreateOptions(RuntimeStartRequest{
		Name:    "analysis",
		Spec:    spec,
		WorkDir: t.TempDir(),
		Port:    18888,
		Token:   "",
		BaseURL: "/notebooks/analysis/proxy/",
	}); err == nil {
		t.Fatal("expected unallowed volume error")
	}
}

func TestDockerRuntimePrepWrapsLaunchCommand(t *testing.T) {
	rt, err := newDockerRuntimeWithClient(DockerConfig{Image: "jupyter/scipy-notebook:latest"}, "test-agent", &fakeDockerClient{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	var spec notebook.NotebookServerSpec
	spec.Metadata.Name = "analysis"
	spec.Spec.Docker = &notebook.NotebookDockerSpec{}
	spec.Spec.Prepare = &notebook.NotebookPrepareSpec{
		Steps: []notebook.NotebookPrepareStep{
			{Type: notebook.PrepareStepCommand, Backend: notebook.PrepareBackendDocker, Command: []string{"bash", "-lc", "echo docker prep"}},
		},
	}

	opts, err := rt.containerCreateOptions(RuntimeStartRequest{
		Name:    "analysis",
		Spec:    spec,
		WorkDir: t.TempDir(),
		Port:    18888,
		Token:   "",
		BaseURL: "/notebooks/analysis/proxy/",
	})
	if err != nil {
		t.Fatalf("container options: %v", err)
	}
	if len(opts.Config.Entrypoint) != 1 || opts.Config.Entrypoint[0] != "/bin/sh" {
		t.Fatalf("entrypoint = %#v", opts.Config.Entrypoint)
	}
	if len(opts.Config.Cmd) != 2 || opts.Config.Cmd[0] != "-lc" {
		t.Fatalf("cmd = %#v", opts.Config.Cmd)
	}
	if !strings.Contains(opts.Config.Cmd[1], "echo docker prep") {
		t.Fatalf("wrapped script = %q", opts.Config.Cmd[1])
	}
}

func TestDockerContainerNameSanitization(t *testing.T) {
	got := dockerContainerName("Team A/Notebook@2026")
	if got != "piper-notebook-Team-A-Notebook-2026" {
		t.Fatalf("container name = %q", got)
	}
}

func TestNewDockerClientUsesDockerDesktopSocketWhenPresent(t *testing.T) {
	if os.Getenv("DOCKER_HOST") != "" {
		t.Skip("DOCKER_HOST is set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(home, ".docker", "run", "docker.sock")
	if info, err := os.Stat(socket); err != nil || info.IsDir() {
		t.Skip("Docker Desktop socket not present")
	}
	cli, err := newDockerClient()
	if err != nil {
		t.Fatalf("newDockerClient: %v", err)
	}
	if got, want := cli.DaemonHost(), "unix://"+socket; got != want {
		t.Fatalf("daemon host = %q, want %q", got, want)
	}
}

func TestDockerGPUResources(t *testing.T) {
	allDS := &notebook.NotebookDockerSpec{
		Deploy: &notebook.DockerDeploySpec{
			Resources: notebook.DockerDeployResources{
				Reservations: notebook.DockerReservations{
					Devices: []notebook.DockerDeviceSpec{{
						Driver:       "nvidia",
						Count:        "all",
						Capabilities: []string{"gpu"},
					}},
				},
			},
		},
	}
	all, err := dockerResources(DockerConfig{}, allDS)
	if err != nil {
		t.Fatal(err)
	}
	if all.resources.DeviceRequests[0].Count != -1 {
		t.Fatalf("all GPU count = %d", all.resources.DeviceRequests[0].Count)
	}

	selectedDS := &notebook.NotebookDockerSpec{
		Deploy: &notebook.DockerDeploySpec{
			Resources: notebook.DockerDeployResources{
				Reservations: notebook.DockerReservations{
					Devices: []notebook.DockerDeviceSpec{{
						Driver:       "nvidia",
						DeviceIDs:    []string{"0", "1"},
						Capabilities: []string{"gpu"},
					}},
				},
			},
		},
	}
	selected, err := dockerResources(DockerConfig{}, selectedDS)
	if err != nil {
		t.Fatal(err)
	}
	if got := selected.resources.DeviceRequests[0].DeviceIDs; len(got) != 2 || got[0] != "0" || got[1] != "1" {
		t.Fatalf("GPU ids = %#v", got)
	}
}

func TestDockerRecoverRegistersBeforeWatching(t *testing.T) {
	cli := &recoveryDockerClient{
		items: []container.Summary{{
			ID:    "running-id",
			State: container.StateRunning,
			Labels: map[string]string{
				dockerManagedLabel:  "true",
				dockerNotebookLabel: "demo",
				dockerWorkerLabel:   "worker-1",
			},
			Ports: []container.PortSummary{{PrivatePort: 8888, PublicPort: 18888, Type: "tcp"}},
		}},
	}
	rt, err := newDockerRuntimeWithClient(DockerConfig{}, "worker-1", cli)
	if err != nil {
		t.Fatal(err)
	}

	registered := false
	cli.onWait = func() {
		if !registered {
			t.Error("ContainerWait attached before worker registration")
		}
	}
	if err := rt.Recover(context.Background(), func(rec recoveredRuntime) func(string) {
		registered = true
		if rec.Name != "demo" || rec.Port != 18888 {
			t.Fatalf("recovered = %#v", rec)
		}
		return func(string) {}
	}, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestDockerRecoverOnlyUsesCurrentWorkerContainers(t *testing.T) {
	cli := &recoveryDockerClient{
		items: []container.Summary{
			{ID: "other", State: container.StateRunning, Labels: map[string]string{
				dockerManagedLabel: "true", dockerNotebookLabel: "other", dockerWorkerLabel: "worker-2",
			}},
		},
	}
	rt, err := newDockerRuntimeWithClient(DockerConfig{}, "worker-1", cli)
	if err != nil {
		t.Fatal(err)
	}

	var exits []string
	if err := rt.Recover(context.Background(), func(recoveredRuntime) func(string) {
		t.Fatal("another worker's container should not be recovered")
		return func(string) {}
	}, func(name, status string) {
		exits = append(exits, name+":"+status)
	}); err != nil {
		t.Fatal(err)
	}
	if len(exits) != 0 {
		t.Fatalf("exits = %v", exits)
	}
	if len(cli.removed) != 0 {
		t.Fatalf("removed = %v", cli.removed)
	}
}

func TestDockerRuntimeRequiresWorkerID(t *testing.T) {
	if _, err := newDockerRuntimeWithClient(DockerConfig{}, "", &fakeDockerClient{}); err == nil {
		t.Fatal("expected empty worker ID to be rejected")
	}
}

type fakeDockerClient struct{}

func (f *fakeDockerClient) ContainerCreate(context.Context, dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error) {
	return dockerclient.ContainerCreateResult{ID: "container-id"}, nil
}

func (f *fakeDockerClient) ContainerInspect(context.Context, string, dockerclient.ContainerInspectOptions) (dockerclient.ContainerInspectResult, error) {
	return dockerclient.ContainerInspectResult{}, nil
}

func (f *fakeDockerClient) ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error) {
	return dockerclient.ContainerStartResult{}, nil
}

func (f *fakeDockerClient) ContainerStop(context.Context, string, dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error) {
	return dockerclient.ContainerStopResult{}, nil
}

func (f *fakeDockerClient) ContainerRemove(context.Context, string, dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error) {
	return dockerclient.ContainerRemoveResult{}, nil
}

func (f *fakeDockerClient) ContainerList(context.Context, dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{}, nil
}

func (f *fakeDockerClient) ContainerWait(context.Context, string, dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult {
	result := make(chan container.WaitResponse, 1)
	errs := make(chan error, 1)
	result <- container.WaitResponse{StatusCode: 0}
	return dockerclient.ContainerWaitResult{Result: result, Error: errs}
}

type recoveryDockerClient struct {
	mu      sync.Mutex
	items   []container.Summary
	removed []string
	onWait  func()
}

func (f *recoveryDockerClient) ContainerCreate(context.Context, dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error) {
	return dockerclient.ContainerCreateResult{}, nil
}
func (f *recoveryDockerClient) ContainerInspect(context.Context, string, dockerclient.ContainerInspectOptions) (dockerclient.ContainerInspectResult, error) {
	return dockerclient.ContainerInspectResult{Container: container.InspectResponse{State: &container.State{ExitCode: 0}}}, nil
}
func (f *recoveryDockerClient) ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error) {
	return dockerclient.ContainerStartResult{}, nil
}
func (f *recoveryDockerClient) ContainerStop(context.Context, string, dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error) {
	return dockerclient.ContainerStopResult{}, nil
}
func (f *recoveryDockerClient) ContainerRemove(_ context.Context, id string, _ dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error) {
	f.mu.Lock()
	f.removed = append(f.removed, id)
	f.mu.Unlock()
	return dockerclient.ContainerRemoveResult{}, nil
}
func (f *recoveryDockerClient) ContainerList(context.Context, dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{Items: f.items}, nil
}
func (f *recoveryDockerClient) ContainerWait(context.Context, string, dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult {
	if f.onWait != nil {
		f.onWait()
	}
	return dockerclient.ContainerWaitResult{
		Result: make(chan container.WaitResponse),
		Error:  make(chan error),
	}
}
