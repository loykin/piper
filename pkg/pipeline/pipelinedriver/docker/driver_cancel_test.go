package dockerdriver

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"

	"github.com/loykin/piper/internal/proto"
	pipelinedriver "github.com/loykin/piper/pkg/pipeline/pipelinedriver"
	"github.com/loykin/piper/pkg/pipeline/pipelinedriver/drivertest"
)

// TestDockerDriverCancelDuringStartStopsJustCreatedContainer mirrors
// worker.go dispatch's canceledMidStart sequence (Start followed immediately
// by Stop) and freezes fed.md 13.1's "cancel during start" behavior.
func TestDockerDriverCancelDuringStartStopsJustCreatedContainer(t *testing.T) {
	cli := &captureDockerClient{}
	d := NewWithClient(Config{RuntimeID: "worker-1", ResultDir: t.TempDir()}, cli)
	handle, err := d.Start(context.Background(), &proto.Task{
		ID: "run-1:train", RunID: "run-1", StepName: "train", Attempt: 1,
	}, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := d.Stop(context.Background(), handle, time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if cli.stoppedID != "container-123456789" {
		t.Fatalf("stoppedID = %q, want container-123456789", cli.stoppedID)
	}
	if cli.removedID != "container-123456789" {
		t.Fatalf("removedID = %q, want container-123456789", cli.removedID)
	}
	if _, err := os.Stat(handle.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("task file still exists after Stop: %v", err)
	}
	d.mu.Lock()
	_, active := d.active[handle.RuntimeKey]
	d.mu.Unlock()
	if active {
		t.Fatalf("runtime key %q remained active after Stop", handle.RuntimeKey)
	}
}

// blockingDockerClient simulates a genuinely running container: ContainerWait
// never fires on its own, so only ctx cancellation (not a container that
// happens to have already finished) unblocks Wait.
type blockingDockerClient struct {
	dockerclient.APIClient
	stoppedID string
	removedID string
}

func (c *blockingDockerClient) ContainerCreate(_ context.Context, _ dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error) {
	return dockerclient.ContainerCreateResult{ID: "container-blocking"}, nil
}

func (c *blockingDockerClient) ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error) {
	return dockerclient.ContainerStartResult{}, nil
}

func (c *blockingDockerClient) ContainerWait(context.Context, string, dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult {
	return dockerclient.ContainerWaitResult{
		Error:  make(chan error),
		Result: make(chan container.WaitResponse),
	}
}

func (c *blockingDockerClient) ContainerStop(_ context.Context, id string, _ dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error) {
	c.stoppedID = id
	return dockerclient.ContainerStopResult{}, nil
}

func (c *blockingDockerClient) ContainerRemove(_ context.Context, id string, _ dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error) {
	c.removedID = id
	return dockerclient.ContainerRemoveResult{}, nil
}

// TestDockerDriverCancelWhileRunningUnblocksWaitAndRemovesContainer mirrors
// worker.go's cancelRun (cancel the shared ctx, then Stop the handle) and
// freezes fed.md 13.1's "cancel while running" behavior. It also pins
// docker's actual (not idealized) divergence from k8s: ctx cancellation
// alone does NOT clean up the container — only an explicit Stop does.
func TestDockerDriverCancelWhileRunningUnblocksWaitAndRemovesContainer(t *testing.T) {
	cli := &blockingDockerClient{}
	d := NewWithClient(Config{RuntimeID: "worker-1", ResultDir: t.TempDir()}, cli)
	handle, err := d.Start(context.Background(), &proto.Task{
		ID: "run-1:train", RunID: "run-1", StepName: "train", Attempt: 1,
	}, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Already-cancelled ctx: deterministic — ContainerWait never fires on its own.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := drivertest.MustWait(t, ctx, d, handle, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want context.Canceled", err)
	}

	d.mu.Lock()
	_, stillActive := d.active[handle.RuntimeKey]
	d.mu.Unlock()
	if !stillActive {
		t.Fatal("runtime key was removed on ctx-cancel alone; docker's Wait must leave cleanup to a separate Stop call")
	}
	if _, err := os.Stat(handle.TaskPath); err != nil {
		t.Fatalf("task file should still exist before an explicit Stop: %v", err)
	}

	if err := d.Stop(context.Background(), handle, time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if cli.stoppedID != "container-blocking" || cli.removedID != "container-blocking" {
		t.Fatalf("stoppedID=%q removedID=%q, want container-blocking for both", cli.stoppedID, cli.removedID)
	}
	if _, err := os.Stat(handle.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("task file still exists after Stop: %v", err)
	}
}
