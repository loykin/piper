package docker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"

	dockerinfra "github.com/loykin/piper/internal/docker"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/serving"
	servingdriver "github.com/loykin/piper/pkg/serving/worker/driver"
	"github.com/loykin/piper/pkg/serving/worker/driver/drivertest"
)

func TestDockerDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() servingdriver.Driver {
		d, err := NewWithClient(Config{WorkerID: "contract-test"}, &recoveryClient{})
		if err != nil {
			t.Fatalf("NewWithClient: %v", err)
		}
		return d
	})
}

func TestDockerRecoverableContract(t *testing.T) {
	drivertest.RunRecoverableContract(t, func() interface {
		servingdriver.Driver
		servingdriver.Recoverable
	} {
		d, err := NewWithClient(Config{WorkerID: "contract-test"}, &recoveryClient{})
		if err != nil {
			t.Fatalf("NewWithClient: %v", err)
		}
		return d
	})
}

var _ servingdriver.Driver = (*Driver)(nil)
var _ servingdriver.Recoverable = (*Driver)(nil)

type recoveryClient struct {
	dockerinfra.API
	items []container.Summary
}

func (c *recoveryClient) ContainerList(context.Context, dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{Items: c.items}, nil
}
func (c *recoveryClient) ContainerInspect(context.Context, string, dockerclient.ContainerInspectOptions) (dockerclient.ContainerInspectResult, error) {
	return dockerclient.ContainerInspectResult{Container: container.InspectResponse{State: &container.State{ExitCode: 0}}}, nil
}
func (c *recoveryClient) ContainerRemove(context.Context, string, dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error) {
	return dockerclient.ContainerRemoveResult{}, nil
}

func TestRecoverReportsTerminalContainer(t *testing.T) {
	cli := &recoveryClient{items: []container.Summary{{
		ID: "container-1", State: container.StateExited,
		Labels: map[string]string{
			dockerManagedLabel: "true", dockerServingLabel: "demo", dockerProjectLabel: "project-a",
			dockerRuntimeLabel: "project-a__demo", dockerWorkerLabel: "worker-1",
		},
	}}}
	d, err := NewWithClient(Config{WorkerID: "worker-1"}, cli)
	if err != nil {
		t.Fatal(err)
	}
	var recovered bool
	var terminal servingdriver.RecoveredHandle
	var status string
	if err := d.Recover(context.Background(), func(servingdriver.RecoveredHandle) func(string) {
		recovered = true
		return func(string) {}
	}, func(handle servingdriver.RecoveredHandle, got string) {
		terminal, status = handle, got
	}); err != nil {
		t.Fatal(err)
	}
	if recovered {
		t.Fatal("terminal container was reported as recovered")
	}
	if terminal.RuntimeName != "project-a__demo" || status != serving.StatusStopped {
		t.Fatalf("terminal = %#v, status = %q", terminal, status)
	}
}

func TestDeployAppliesDockerResourcesAndGPUs(t *testing.T) {
	cli := &deployClient{}
	d, err := NewWithClient(Config{WorkerID: "worker-1"}, cli)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Deploy(context.Background(), servingdriver.DeployRequest{
		ProjectID:   "project-a",
		Name:        "demo",
		RuntimeName: "project-a__demo",
		Image:       "model:test",
		Command:     []string{"serve"},
		Port:        18080,
		Docker: &manifest.DriverDockerSpec{
			CPUs:     "1.5",
			MemLimit: "512m",
			Deploy: &manifest.DockerDeploySpec{Resources: manifest.DockerDeployResources{
				Reservations: &manifest.DockerReservations{Devices: []manifest.DockerDevice{{
					DeviceIDs:    []string{"0", "1"},
					Capabilities: []string{"gpu"},
				}}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	host := cli.create.HostConfig
	if host.Resources.NanoCPUs != 1_500_000_000 {
		t.Fatalf("NanoCPUs = %d", host.Resources.NanoCPUs)
	}
	if host.Resources.Memory != 512*1024*1024 {
		t.Fatalf("Memory = %d", host.Resources.Memory)
	}
	if len(host.Resources.DeviceRequests) != 1 {
		t.Fatalf("DeviceRequests = %#v", host.Resources.DeviceRequests)
	}
	if got := host.Resources.DeviceRequests[0].DeviceIDs; len(got) != 2 || got[0] != "0" || got[1] != "1" {
		t.Fatalf("DeviceIDs = %#v", got)
	}
}

func TestDeployAppliesProcessGPUSelectorWhenDockerHasNoDeviceRequests(t *testing.T) {
	cli := &deployClient{}
	d, err := NewWithClient(Config{WorkerID: "worker-1"}, cli)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Deploy(context.Background(), servingdriver.DeployRequest{
		ProjectID:   "project-a",
		Name:        "demo",
		RuntimeName: "project-a__demo",
		Image:       "model:test",
		Command:     []string{"serve"},
		Port:        18080,
		GPUs:        "all",
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(cli.create.HostConfig.Resources.DeviceRequests) != 1 || cli.create.HostConfig.Resources.DeviceRequests[0].Count != -1 {
		t.Fatalf("DeviceRequests = %#v", cli.create.HostConfig.Resources.DeviceRequests)
	}
}

func TestDeployMountsModelDirectoryReadOnly(t *testing.T) {
	cli := &deployClient{}
	d, err := NewWithClient(Config{WorkerID: "worker-1"}, cli)
	if err != nil {
		t.Fatal(err)
	}
	modelDir := t.TempDir()
	_, err = d.Deploy(context.Background(), servingdriver.DeployRequest{
		ProjectID: "project-a", Name: "demo", RuntimeName: "project-a__demo",
		Image: "model:test", Command: []string{"serve"}, Port: 18080,
		ModelDir: modelDir,
		Env:      map[string]string{"PIPER_MODEL_DIR": servingdriver.ContainerModelDir},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	mounts := cli.create.HostConfig.Mounts
	if len(mounts) != 1 {
		t.Fatalf("mounts = %#v", mounts)
	}
	wantSource, _ := filepath.Abs(modelDir)
	if mounts[0].Source != wantSource || mounts[0].Target != servingdriver.ContainerModelDir || !mounts[0].ReadOnly {
		t.Fatalf("model mount = %#v", mounts[0])
	}
}

type deployClient struct {
	dockerinfra.API
	create dockerclient.ContainerCreateOptions
}

func (c *deployClient) ContainerList(context.Context, dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{}, nil
}

func (c *deployClient) ContainerCreate(_ context.Context, opts dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error) {
	c.create = opts
	return dockerclient.ContainerCreateResult{ID: "container-123456789"}, nil
}

func (c *deployClient) ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error) {
	return dockerclient.ContainerStartResult{}, nil
}

func (c *deployClient) ContainerWait(context.Context, string, dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult {
	return dockerclient.ContainerWaitResult{
		Result: make(chan container.WaitResponse),
		Error:  make(chan error),
	}
}
