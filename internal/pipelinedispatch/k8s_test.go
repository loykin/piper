package pipelinedispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
)

func directK8sTask(t *testing.T, runID string, placement manifest.PlacementSpec) *proto.Task {
	t.Helper()
	pl := pipeline.Pipeline{
		Spec: pipeline.PipelineSpec{
			Defaults: &pipeline.PipelineDefaults{Driver: manifest.DriverSpec{
				Placement: placement,
				K8s:       &manifest.DriverK8sSpec{Image: "python:3.12", Namespace: "runs"},
			}},
			Steps: []pipeline.Step{{Name: "train", Run: pipeline.Run{Command: []string{"python", "train.py"}}}},
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

func TestK8sBackendDispatchesDirectlyAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := fake.NewSimpleClientset()
	backend, err := NewK8sBackend(K8sBackendConfig{
		Context: ctx, Client: client, Namespaces: []string{"runs"}, PipelineRunnerImage: "piper:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	task := directK8sTask(t, "run-1", manifest.PlacementSpec{Runtime: "k8s"})
	if err := backend.Dispatch(ctx, task); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	jobs, err := client.BatchV1().Jobs("runs").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs after dispatch = %d, want 1", len(jobs.Items))
	}
	if err := backend.CancelRun(ctx, "run-1"); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	jobs, err = client.BatchV1().Jobs("runs").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("jobs after cancel = %d, want 0", len(jobs.Items))
	}
}

func TestK8sBackendRejectsRemotePlacement(t *testing.T) {
	tests := []struct {
		name      string
		placement manifest.PlacementSpec
		want      string
	}{
		{name: "worker", placement: manifest.PlacementSpec{Worker: "remote-1", Runtime: "k8s"}, want: "placement.worker is not supported"},
		{name: "label", placement: manifest.PlacementSpec{Label: "gpu", Runtime: "k8s"}, want: "placement.label is not supported"},
		{name: "other runtime", placement: manifest.PlacementSpec{Runtime: "docker"}, want: "placement.runtime must be k8s or empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			backend, err := NewK8sBackend(K8sBackendConfig{Client: client, Namespaces: []string{"runs"}})
			if err != nil {
				t.Fatal(err)
			}
			err = backend.Dispatch(context.Background(), directK8sTask(t, "run-2", tt.placement))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Dispatch() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestK8sBackendCancellationTombstoneBlocksLateDispatch(t *testing.T) {
	client := fake.NewSimpleClientset()
	backend, err := NewK8sBackend(K8sBackendConfig{Client: client, Namespaces: []string{"runs"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CancelRun(context.Background(), "run-3"); err != nil {
		t.Fatal(err)
	}
	err = backend.Dispatch(context.Background(), directK8sTask(t, "run-3", manifest.PlacementSpec{Runtime: "k8s"}))
	if err == nil || !strings.Contains(err.Error(), "canceled before dispatch") {
		t.Fatalf("Dispatch() error = %v, want cancellation tombstone", err)
	}
}
