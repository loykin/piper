package docker

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"

	dockerinfra "github.com/piper/piper/internal/docker"
	"github.com/piper/piper/pkg/serving"
	servingdriver "github.com/piper/piper/pkg/serving/worker/driver"
	"github.com/piper/piper/pkg/serving/worker/driver/drivertest"
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
