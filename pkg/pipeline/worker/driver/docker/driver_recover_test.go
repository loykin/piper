package dockerdriver

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"
)

// recoverListDockerClient returns a fixed set of containers from
// ContainerList, standing in for a Docker daemon that survived a worker
// restart with piper-managed containers still present.
type recoverListDockerClient struct {
	dockerclient.APIClient
	items []container.Summary
}

func (c *recoverListDockerClient) ContainerList(_ context.Context, _ dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{Items: c.items}, nil
}

// TestDockerDriverRecoverReattachesRunningContainer freezes fed.md 13.1's
// "recovery after restart" behavior for docker: a fresh Driver instance
// re-attaches to a still-running container discovered via its piper.*
// labels, matching the k8s equivalent
// (TestDriverRecoverScansAllowedNamespaceAndMetadata).
func TestDockerDriverRecoverReattachesRunningContainer(t *testing.T) {
	labels := map[string]string{
		labelManaged:    "true",
		labelPipeline:   "true",
		labelWorkerID:   "worker-1",
		labelRuntimeKey: "worker-1-run-1-train-a1",
		labelTaskID:     "run-1:train",
		labelRunID:      "run-1",
		labelStepName:   "train",
		labelAttempt:    "1",
		labelResultPath: "/host/results/worker-1-run-1-train-a1.result.json",
		labelTaskPath:   "/host/results/worker-1-run-1-train-a1.task.json",
	}
	cli := &recoverListDockerClient{items: []container.Summary{
		{ID: "container-recovered", Labels: labels},
	}}
	d := NewWithClient(Config{WorkerID: "worker-1"}, cli)

	handles, err := d.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(handles) != 1 {
		t.Fatalf("handles = %d, want 1", len(handles))
	}
	got := handles[0]
	if got.RuntimeKey != labels[labelRuntimeKey] || got.TaskID != labels[labelTaskID] ||
		got.RunID != labels[labelRunID] || got.StepName != labels[labelStepName] ||
		got.Attempt != 1 || got.ResultPath != labels[labelResultPath] ||
		got.TaskPath != labels[labelTaskPath] {
		t.Fatalf("recovered handle = %#v", got)
	}
	d.mu.Lock()
	containerID, tracked := d.active[got.RuntimeKey]
	d.mu.Unlock()
	if !tracked || containerID != "container-recovered" {
		t.Fatalf("active[%q] = %q, tracked=%v, want container-recovered tracked", got.RuntimeKey, containerID, tracked)
	}
}
