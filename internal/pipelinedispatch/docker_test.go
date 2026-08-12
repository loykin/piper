package pipelinedispatch

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

func directRuntimeTask(t *testing.T, runID string, placement manifest.PlacementSpec, driverSpec manifest.DriverSpec) *proto.Task {
	t.Helper()
	driverSpec.Placement = placement
	pl := pipeline.Pipeline{
		Spec: pipeline.PipelineSpec{
			Defaults: &pipeline.PipelineDefaults{Driver: driverSpec},
			Steps:    []pipeline.Step{{Name: "train", Run: pipeline.Run{Command: []string{"python", "train.py"}}}},
		},
	}
	pipelineJSON, err := json.Marshal(pl)
	if err != nil {
		t.Fatal(err)
	}
	stepJSON, err := json.Marshal(pl.Spec.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	return &proto.Task{
		ID: runID + ":train", RunID: runID, StepName: "train", Attempt: 1,
		Pipeline: pipelineJSON, Step: stepJSON,
	}
}

func dockerDriverSpec() manifest.DriverSpec {
	return manifest.DriverSpec{Docker: &manifest.DriverDockerSpec{Image: "python:3.11"}}
}

func TestDockerBackendDispatchesDirectlyAndCancels(t *testing.T) {
	var mu sync.Mutex
	started := map[string]pdriver.Handle{}
	stopped := map[string]bool{}
	waitBlockers := map[string]chan struct{}{}

	driver := &fakeDirectDriver{
		startFn: func(_ context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error) {
			mu.Lock()
			defer mu.Unlock()
			h := pdriver.Handle{RuntimeKey: spec.RuntimeKey, RunID: task.RunID, TaskID: task.ID}
			started[spec.RuntimeKey] = h
			waitBlockers[spec.RuntimeKey] = make(chan struct{})
			return h, nil
		},
		waitFn: func(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error) {
			mu.Lock()
			ch := waitBlockers[handle.RuntimeKey]
			mu.Unlock()
			select {
			case <-ch:
				return pdriver.Exit{}, nil
			case <-ctx.Done():
				return pdriver.Exit{}, ctx.Err()
			}
		},
		stopFn: func(_ context.Context, handle pdriver.Handle) error {
			mu.Lock()
			stopped[handle.RuntimeKey] = true
			ch := waitBlockers[handle.RuntimeKey]
			mu.Unlock()
			if ch != nil {
				close(ch)
			}
			return nil
		},
	}

	backend, err := NewDockerBackend(DockerBackendConfig{Concurrency: 4, Driver: driver})
	if err != nil {
		t.Fatal(err)
	}

	task := directRuntimeTask(t, "run-1", manifest.PlacementSpec{Runtime: "docker"}, dockerDriverSpec())
	if err := backend.Dispatch(context.Background(), task); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !waitUntilTrue(2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(started) == 1
	}) {
		t.Fatal("container was not started")
	}

	if err := backend.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	if !waitUntilTrue(2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(stopped) == 1
	}) {
		t.Fatal("container was not stopped after cancel")
	}
}

func TestDockerBackendRejectsRemotePlacement(t *testing.T) {
	tests := []struct {
		name      string
		placement manifest.PlacementSpec
		want      string
	}{
		{name: "worker", placement: manifest.PlacementSpec{Worker: "remote-1", Runtime: "docker"}, want: "placement.worker is not supported"},
		{name: "label", placement: manifest.PlacementSpec{Label: "gpu", Runtime: "docker"}, want: "placement.label is not supported"},
		{name: "other runtime", placement: manifest.PlacementSpec{Runtime: "k8s"}, want: "placement.runtime must be docker or empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := NewDockerBackend(DockerBackendConfig{Concurrency: 4, Driver: &fakeDirectDriver{}})
			if err != nil {
				t.Fatal(err)
			}
			err = backend.Dispatch(context.Background(), directRuntimeTask(t, "run-2", tt.placement, dockerDriverSpec()))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Dispatch() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDockerBackendCancellationTombstoneBlocksLateDispatch(t *testing.T) {
	backend, err := NewDockerBackend(DockerBackendConfig{Concurrency: 4, Driver: &fakeDirectDriver{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CancelRun(context.Background(), "run-3"); err != nil {
		t.Fatal(err)
	}
	err = backend.Dispatch(context.Background(), directRuntimeTask(t, "run-3", manifest.PlacementSpec{Runtime: "docker"}, dockerDriverSpec()))
	if err == nil || !strings.Contains(err.Error(), "canceled before dispatch") {
		t.Fatalf("Dispatch() error = %v, want cancellation tombstone", err)
	}
}
