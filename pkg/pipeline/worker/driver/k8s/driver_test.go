package k8sdriver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/manifest"
	k8smanifest "github.com/loykin/piper/pkg/manifest/k8s"
	"github.com/loykin/piper/pkg/pipeline"
	agentpkg "github.com/loykin/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	"github.com/loykin/piper/pkg/pipeline/worker/driver/drivertest"
)

var _ pdriver.Driver = (*Driver)(nil)

func TestK8sDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() pdriver.Driver {
		d, err := New(Config{WorkerID: "contract-test", K8sClient: fake.NewSimpleClientset()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	})
}

func TestDriverStartWaitUsesDriverResolvedExecution(t *testing.T) {
	client := fake.NewSimpleClientset()
	drv, err := New(Config{
		WorkerID:   "worker-1",
		Namespaces: []string{"jobs"},
		AgentImage: "piper:test",
		K8sClient:  client,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := testTask(t, "jobs")
	task.Env = []string{"PIPER_GIT_USER=test-user", "PIPER_GIT_TOKEN=test-token"}

	handle, err := drv.Start(context.Background(), task, pdriver.ExecSpec{
		RuntimeKey:   "worker-1-run-1-train-a1",
		Image:        "python:3.12", // pre-resolved by the worker layer
		Namespace:    "jobs",        // pre-resolved by the worker layer
		StorageToken: "storage-token",
		StorageURL:   "s3://bucket",
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
	if env := job.Spec.Template.Spec.Containers[0].Env; len(env) != 0 {
		t.Fatalf("job env = %#v", env)
	}
	args := job.Spec.Template.Spec.Containers[0].Args
	for _, want := range []string{
		"--storage-token=storage-token",
		"--storage-url=s3://bucket",
		"--task-file=/piper-task/task.json",
		"--result-file=/dev/termination-log",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("job args missing %q: %v", want, args)
		}
	}
	for _, notWant := range []string{"--git-user", "--git-token", "--task="} {
		for _, arg := range args {
			if strings.HasPrefix(arg, notWant) {
				t.Fatalf("job args must not expose git credentials as CLI flags: %v", args)
			}
		}
	}
	secret, err := client.CoreV1().Secrets("jobs").Get(context.Background(), handle.RuntimeKey+"-task", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secret.Data["task.json"]), "test-token") {
		t.Fatal("task secret did not contain task env")
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

func TestDriverRecoverScansAllowedNamespaceAndMetadata(t *testing.T) {
	client := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "piper-run-step-a2",
			Namespace: "dynamic-jobs",
			Labels: map[string]string{
				k8smanifest.LabelManagedBy: k8smanifest.ManagedByPiper,
				k8smanifest.LabelWorkerID:  "worker-1",
			},
			Annotations: map[string]string{
				k8smanifest.AnnotationTaskID:   "run/1:step",
				k8smanifest.AnnotationRunID:    "run/1",
				k8smanifest.AnnotationStepName: "step",
				k8smanifest.AnnotationAttempt:  "2",
			},
		},
	})
	drv, err := New(Config{
		WorkerID:   "worker-1",
		Namespaces: []string{"dynamic-jobs"},
		K8sClient:  client,
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

// TestK8sDriverCancelDuringStartDeletesJustCreatedJob mirrors worker.go
// dispatch's canceledMidStart sequence (Start followed immediately by Stop)
// and freezes fed.md 13.1's "cancel during start" behavior.
func TestK8sDriverCancelDuringStartDeletesJustCreatedJob(t *testing.T) {
	client := fake.NewSimpleClientset()
	drv, err := New(Config{
		WorkerID:   "worker-1",
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
		Image:      "python:3.12",
		Namespace:  "jobs",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := drv.Stop(context.Background(), handle, time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := client.BatchV1().Jobs("jobs").Get(context.Background(), handle.RuntimeKey, metav1.GetOptions{}); !k8serrors.IsNotFound(err) {
		t.Fatalf("Get job after Stop: err = %v, want NotFound", err)
	}
	drv.mu.Lock()
	_, hasWaiter := drv.waiters[handle.RuntimeKey]
	_, hasTaskKey := drv.taskToKey[task.ID]
	_, hasNamespace := drv.namespaceByKey[handle.RuntimeKey]
	drv.mu.Unlock()
	if hasWaiter || hasTaskKey || hasNamespace {
		t.Fatalf("driver state not forgotten after Stop: waiter=%v taskKey=%v namespace=%v", hasWaiter, hasTaskKey, hasNamespace)
	}
}

// TestK8sDriverCancelWhileRunningForgetsHandleBeforeStopCanActOnIt mirrors
// worker.go's cancelRun (cancel the shared ctx, then Stop the handle) and
// freezes fed.md 13.1's "cancel while running" behavior — including a
// currently-real divergence from docker, not an idealized one: k8s's own
// Wait already calls forget() (clearing waiters/taskToKey/namespaceByKey) on
// its ctx-cancel branch. Because Stop() looks up the Job's namespace from
// that same namespaceByKey map, a Stop that arrives after Wait has already
// unblocked on ctx-cancel finds no namespace and errors out WITHOUT deleting
// the Job — the Job is orphaned in the cluster. This is the actual behavior
// today; fixing it is out of scope for this contract-freezing pass.
func TestK8sDriverCancelWhileRunningForgetsHandleBeforeStopCanActOnIt(t *testing.T) {
	client := fake.NewSimpleClientset()
	drv, err := New(Config{
		WorkerID:   "worker-1",
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
		Image:      "python:3.12",
		Namespace:  "jobs",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Already-cancelled ctx: deterministic — no reconcile ever signals the waiter channel.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := drivertest.MustWait(t, ctx, drv, handle, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want context.Canceled", err)
	}
	drv.mu.Lock()
	_, hasWaiter := drv.waiters[handle.RuntimeKey]
	_, hasNamespace := drv.namespaceByKey[handle.RuntimeKey]
	drv.mu.Unlock()
	if hasWaiter || hasNamespace {
		t.Fatalf("k8s driver's Wait must forget the handle (waiter+namespace) on its own ctx-cancel branch: hasWaiter=%v hasNamespace=%v", hasWaiter, hasNamespace)
	}

	// A Stop arriving after forget() has already run cannot resolve a
	// namespace, so it errors and leaves the Job in place — it is not deleted.
	if err := drv.Stop(context.Background(), handle, time.Second); err == nil {
		t.Fatal("Stop succeeded after forget() already cleared namespaceByKey; expected the current namespace-required error")
	}
	if _, err := client.BatchV1().Jobs("jobs").Get(context.Background(), handle.RuntimeKey, metav1.GetOptions{}); err != nil {
		t.Fatalf("Job was unexpectedly deleted despite Stop erroring out: %v", err)
	}
}

func testTask(t *testing.T, namespace string) *proto.Task {
	t.Helper()
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{
				Image:     "python:3.12",
				Namespace: namespace,
			},
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
