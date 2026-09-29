package pipeline

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
)

func TestPipelineDispatchCreatesJob(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		Store: StoreConfig{},
		K8s: K8sConfig{
			Client:     client,
			Namespaces: []string{"runs", "run-placement"},
			AgentImage: "piper:test",
		},
	})
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{
				Image:     "python:3.12",
				Namespace: "run-placement",
			},
		},
	}
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
				"piper.io/run-id": "run-1",
			},
		},
	})
	a := New(Config{K8s: K8sConfig{Client: client, Namespaces: []string{"runs"}}})

	if err := a.cancelPipelineRun(context.Background(), pipelineCancelRunRequest{RunID: "run-1", Namespace: "runs"}); err != nil {
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

type recordingSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *recordingSink) Append(_, _, _, line string, _ time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
}

func (s *recordingSink) Stop() {}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lines)
}

// A follow stream opened on a just-created pod can succeed with an empty body
// and end at once; streamJobLogs must wait for a started container instead of
// returning with nothing, which lost every log line of a short Job.
func TestStreamJobLogsWaitsForStartedContainer(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "job-a-pod", Namespace: "runs", Labels: map[string]string{"job-name": "job-a"},
	}}
	client := fake.NewSimpleClientset(pod)
	sink := &recordingSink{}
	task := &proto.Task{RunID: "run-1", StepName: "step"}
	done := make(chan struct{})
	go func() {
		streamJobLogs(context.Background(), client, "runs", "job-a", task, sink)
		close(done)
	}()

	time.Sleep(700 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("streamJobLogs returned before the container started")
	default:
	}
	for _, action := range client.Actions() {
		if action.GetSubresource() == "log" {
			t.Fatal("opened a log stream before the container started")
		}
	}

	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name: "step", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}},
	}}
	if _, err := client.CoreV1().Pods("runs").UpdateStatus(context.Background(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamJobLogs did not finish after the container terminated")
	}
	if sink.count() == 0 {
		t.Fatal("no log lines were streamed after the container started")
	}
}
