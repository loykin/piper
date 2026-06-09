package k8sdriver

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
	agentpkg "github.com/piper/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/piper/piper/pkg/pipeline/worker/driver"
)

func TestDriverStartWaitUsesDriverResolvedExecution(t *testing.T) {
	client := fake.NewSimpleClientset()
	drv, err := New(Config{
		WorkerID:   "worker-1",
		Namespace:  "default",
		Namespaces: []string{"jobs"},
		AgentImage: "piper:test",
		K8sClient:  client,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := testTask(t, "jobs")

	handle, err := drv.Start(context.Background(), task, pdriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.12", // pre-resolved by the worker layer
		Namespace:  "jobs",        // pre-resolved by the worker layer
		MasterURL:  "http://master:8080",
		Token:      "token",
		StorageURL: "s3://bucket",
		Env:        []string{"PIPER_GIT_USER=test-user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if handle.RuntimeKey == "" {
		t.Fatal("runtime key is empty")
	}
	job, err := client.BatchV1().Jobs("jobs").Get(context.Background(), handle.RuntimeKey, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := job.Spec.Template.Spec.InitContainers[0].Image; got != "piper:test" {
		t.Fatalf("agent image = %q", got)
	}
	if env := job.Spec.Template.Spec.Containers[0].Env; len(env) != 1 ||
		env[0].Name != "PIPER_GIT_USER" || env[0].Value != "test-user" {
		t.Fatalf("job env = %#v", env)
	}
	args := job.Spec.Template.Spec.Containers[0].Args
	for _, want := range []string{
		"--report-mode=file",
		"--master=http://master:8080",
		"--token=token",
		"--storage-url=s3://bucket",
		"--result-file=/dev/termination-log",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("job args missing %q: %v", want, args)
		}
	}

	resultData, err := agentpkg.WriteAgentResult(proto.TaskResult{
		TaskID:  task.ID,
		Attempt: 1,
		Status:  proto.TaskStatusDone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods("jobs").Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-pod",
			Namespace: "jobs",
			Labels:    map[string]string{"job-name": job.Name},
		},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "step",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Message: string(resultData),
			}},
		}}},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	job.Status.Succeeded = 1
	if _, err := client.BatchV1().Jobs("jobs").UpdateStatus(context.Background(), job, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	drv.reconcileOnce(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exit, err := drv.Wait(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	if exit.Result == nil || exit.Result.Status != proto.TaskStatusDone {
		t.Fatalf("exit result = %#v", exit.Result)
	}
}

func TestDriverRecoverDiscoversDynamicNamespaceAndMetadata(t *testing.T) {
	client := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "piper-run-step-a2",
			Namespace: "dynamic-jobs",
			Labels: map[string]string{
				k8smeta.LabelManagedBy: k8smeta.ManagedByPiper,
				k8smeta.LabelWorkerID:  "worker-1",
			},
			Annotations: map[string]string{
				k8smeta.AnnotationTaskID:   "run/1:step",
				k8smeta.AnnotationRunID:    "run/1",
				k8smeta.AnnotationStepName: "step",
				k8smeta.AnnotationAttempt:  "2",
			},
		},
	})
	drv, err := New(Config{
		WorkerID:  "worker-1",
		Namespace: "default",
		K8sClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}

	handles, err := drv.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(handles) != 1 {
		t.Fatalf("handles = %d, want 1", len(handles))
	}
	got := handles[0]
	if got.RuntimeKey != "piper-run-step-a2" || got.TaskID != "run/1:step" ||
		got.RunID != "run/1" || got.StepName != "step" || got.Attempt != 2 {
		t.Fatalf("recovered handle = %#v", got)
	}
}

func TestDriverRejectsUnsafeTTL(t *testing.T) {
	ttl := int32(10)
	if _, err := New(Config{
		K8sClient:        fake.NewSimpleClientset(),
		TTLAfterFinished: &ttl,
	}); err == nil {
		t.Fatal("expected short TTL to be rejected")
	}
}

func testTask(t *testing.T, namespace string) *proto.Task {
	t.Helper()
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			Image: "python:3.12",
			K8s:   &manifest.DriverK8sSpec{Namespace: namespace},
		},
	}
	step := pipeline.Step{Name: "train"}
	step.Run.Command = []string{"python", "train.py"}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	pipelineJSON, err := json.Marshal(pl)
	if err != nil {
		t.Fatal(err)
	}
	return &proto.Task{
		ID:       "run-1:train",
		RunID:    "run-1",
		StepName: "train",
		Attempt:  1,
		WorkerID: "worker-1",
		Step:     stepJSON,
		Pipeline: pipelineJSON,
	}
}
