package notebookworker

import (
	"context"
	"os"
	"path/filepath"
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
	}, &fakeDockerClient{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	var spec notebook.NotebookServerSpec
	spec.Metadata.Name = "analysis"
	spec.Spec.GPUs = "0,1"
	spec.Spec.Volumes = []string{"datasets"}

	opts, err := rt.containerCreateOptions(RuntimeStartRequest{
		Name:    "analysis",
		Spec:    spec,
		WorkDir: t.TempDir(),
		Port:    18888,
		Token:   "tok",
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
	wantArgs := notebook.JupyterStartArgs("/notebooks/analysis/proxy/", "tok", notebook.ContainerWorkDir, 8888)
	for i, want := range wantArgs {
		if opts.Config.Cmd[i] != want {
			t.Fatalf("cmd[%d] = %q, want %q", i, opts.Config.Cmd[i], want)
		}
	}
}

func TestDockerRuntimeRejectsUnallowedVolume(t *testing.T) {
	rt, err := newDockerRuntimeWithClient(DockerConfig{}, &fakeDockerClient{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	var spec notebook.NotebookServerSpec
	spec.Metadata.Name = "analysis"
	spec.Spec.Volumes = []string{"host-root"}

	if _, err := rt.containerCreateOptions(RuntimeStartRequest{
		Name:    "analysis",
		Spec:    spec,
		WorkDir: t.TempDir(),
		Port:    18888,
		Token:   "tok",
		BaseURL: "/notebooks/analysis/proxy/",
	}); err == nil {
		t.Fatal("expected unallowed volume error")
	}
}

func TestDockerContainerNameSanitization(t *testing.T) {
	got := dockerContainerName("Team A/Notebook@2026")
	if got != "piper-notebook-Team-A-Notebook-2026" {
		t.Fatalf("container name = %q", got)
	}
}

func TestDockerGPUResources(t *testing.T) {
	all, err := dockerResources(DockerConfig{}, "all")
	if err != nil {
		t.Fatal(err)
	}
	if all.resources.DeviceRequests[0].Count != -1 {
		t.Fatalf("all GPU count = %d", all.resources.DeviceRequests[0].Count)
	}
	selected, err := dockerResources(DockerConfig{}, "0,1")
	if err != nil {
		t.Fatal(err)
	}
	if got := selected.resources.DeviceRequests[0].DeviceIDs; len(got) != 2 || got[0] != "0" || got[1] != "1" {
		t.Fatalf("GPU ids = %#v", got)
	}
}

type fakeDockerClient struct{}

func (f *fakeDockerClient) ContainerCreate(context.Context, dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error) {
	return dockerclient.ContainerCreateResult{ID: "container-id"}, nil
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
