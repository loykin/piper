package dockerdriver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
	pipelinedriver "github.com/piper/piper/pkg/pipeline/worker/driver"
	"github.com/piper/piper/pkg/pipeline/worker/driver/drivertest"
)

var _ pipelinedriver.Driver = (*Driver)(nil)

func TestDockerDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() pipelinedriver.Driver {
		return NewWithClient(Config{WorkerID: "contract-test"}, &emptyDockerClient{})
	})
}

// emptyDockerClient implements only the methods exercised by the contract tests
// (ContainerList for Recover on empty state). All other methods panic if called.
type emptyDockerClient struct{ dockerclient.APIClient }

func (c *emptyDockerClient) ContainerList(_ context.Context, _ dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{}, nil
}

func TestDockerDriverAppliesStepResources(t *testing.T) {
	step := pipeline.Step{
		Name: "train",
		Driver: manifest.DriverSpec{Docker: &manifest.DriverDockerSpec{
			Image:    "python:3.11",
			CPUs:     "2",
			MemLimit: "1g",
			ShmSize:  "256m",
			Deploy: &manifest.DockerDeploySpec{Resources: manifest.DockerDeployResources{
				Reservations: &manifest.DockerReservations{Devices: []manifest.DockerDevice{{
					Count:        "1",
					Capabilities: []string{"gpu"},
				}}},
			}},
		}},
		Run: pipeline.Run{Command: []string{"echo", "ok"}},
	}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	cli := &captureDockerClient{}
	d := NewWithClient(Config{WorkerID: "worker-1", ResultDir: t.TempDir()}, cli)
	_, err = d.Start(context.Background(), &proto.Task{
		ID:       "run-1:train",
		RunID:    "run-1",
		StepName: "train",
		Step:     stepJSON,
		Attempt:  1,
		Env:      []string{"APP_TOKEN=secret-value"},
	}, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	host := cli.create.HostConfig
	if host.Resources.NanoCPUs != 2_000_000_000 {
		t.Fatalf("NanoCPUs = %d", host.Resources.NanoCPUs)
	}
	if host.Resources.Memory != 1024*1024*1024 {
		t.Fatalf("Memory = %d", host.Resources.Memory)
	}
	if host.ShmSize != 256*1024*1024 {
		t.Fatalf("ShmSize = %d", host.ShmSize)
	}
	if len(host.Resources.DeviceRequests) != 1 || host.Resources.DeviceRequests[0].Count != 1 {
		t.Fatalf("DeviceRequests = %#v", host.Resources.DeviceRequests)
	}
	if got := cli.create.Config.Env; len(got) != 0 {
		t.Fatalf("container env exposed resolved task env: %#v", got)
	}
	for _, arg := range cli.create.Config.Cmd {
		if arg == "--task-file=/piper-results/worker-1-run-1-train-a1.task.json" {
			continue
		}
		if strings.Contains(arg, "secret-value") || strings.HasPrefix(arg, "--task=") {
			t.Fatalf("container args exposed task payload: %#v", cli.create.Config.Cmd)
		}
	}
}

func TestDockerDriverWaitErrorCleansRuntimeState(t *testing.T) {
	cli := &captureDockerClient{waitErr: errors.New("daemon unavailable")}
	resultDir := t.TempDir()
	d := NewWithClient(Config{WorkerID: "worker-1", ResultDir: resultDir}, cli)
	handle, err := d.Start(context.Background(), &proto.Task{
		ID:       "run-1:train",
		RunID:    "run-1",
		StepName: "train",
		Attempt:  1,
		Env:      []string{"APP_TOKEN=secret-value"},
	}, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(handle.TaskPath); err != nil {
		t.Fatalf("task file missing before Wait: %v", err)
	}

	exit, err := d.Wait(context.Background(), handle)
	if err != nil {
		t.Fatalf("Wait returned transport error: %v", err)
	}
	if exit.InfraFailure == nil {
		t.Fatal("Wait returned nil InfraFailure")
	}
	if cli.removedID == "" {
		t.Fatal("container was not removed")
	}
	if _, err := os.Stat(handle.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("task file still exists after Wait error: %v", err)
	}
	d.mu.Lock()
	_, active := d.active[handle.RuntimeKey]
	d.mu.Unlock()
	if active {
		t.Fatalf("runtime key %q remained active", handle.RuntimeKey)
	}
}

type captureDockerClient struct {
	dockerclient.APIClient
	create    dockerclient.ContainerCreateOptions
	waitErr   error
	removedID string
}

func (c *captureDockerClient) ContainerCreate(_ context.Context, opts dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error) {
	c.create = opts
	return dockerclient.ContainerCreateResult{ID: "container-123456789"}, nil
}

func (c *captureDockerClient) ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error) {
	return dockerclient.ContainerStartResult{}, nil
}

func (c *captureDockerClient) ContainerWait(context.Context, string, dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult {
	errCh := make(chan error, 1)
	resultCh := make(chan container.WaitResponse, 1)
	result := dockerclient.ContainerWaitResult{
		Error:  errCh,
		Result: resultCh,
	}
	if c.waitErr != nil {
		errCh <- c.waitErr
	} else {
		resultCh <- container.WaitResponse{}
	}
	return result
}

func (c *captureDockerClient) ContainerRemove(_ context.Context, id string, _ dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error) {
	c.removedID = id
	return dockerclient.ContainerRemoveResult{}, nil
}
