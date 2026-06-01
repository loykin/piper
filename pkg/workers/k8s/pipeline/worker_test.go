package pipelineworker

import (
	"context"
	"encoding/json"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

func TestPipelineDispatchCreatesJob(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		MasterURL:    "http://piper:8080",
		Client:       client,
		Namespace:    "runs",
		WorkerImage:  "piper:test",
		DefaultImage: "python:3.12",
	})
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults.Image = "python:3.12"
	pl.Spec.Placement.Namespace = "run-placement"
	step := pipeline.Step{Name: "train"}
	step.Run.Command = []string{"python", "train.py"}
	stepJSON, _ := json.Marshal(step)
	pipelineJSON, _ := json.Marshal(pl)

	if err := a.dispatchPipeline(context.Background(), &proto.Task{
		ID:        "run-1:train",
		RunID:     "run-1",
		StepName:  "train",
		Step:      stepJSON,
		Pipeline:  pipelineJSON,
		OutputDir: "/tmp/out",
		WorkDir:   "/tmp/work",
	}); err != nil {
		t.Fatalf("dispatchPipeline returned error: %v", err)
	}
	jobs, err := client.BatchV1().Jobs("run-placement").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs.Items))
	}
	if jobs.Items[0].Spec.Template.Spec.InitContainers[0].Image != "piper:test" {
		t.Fatalf("agent image = %q", jobs.Items[0].Spec.Template.Spec.InitContainers[0].Image)
	}
}

func TestPipelineCancelDeletesJobs(t *testing.T) {
	client := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-1",
			Namespace: "runs",
			Labels: map[string]string{
				"piper/run-id": "run-1",
			},
		},
	})
	a := New(Config{Client: client, Namespace: "runs"})

	if err := a.cancelPipelineRun(context.Background(), pipelineCancelRunRequest{RunID: "run-1"}); err != nil {
		t.Fatalf("cancelPipelineRun returned error: %v", err)
	}
	jobs, err := client.BatchV1().Jobs("runs").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs.Items))
	}
}
