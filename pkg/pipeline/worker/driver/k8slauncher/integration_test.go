//go:build integration

package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline"
	taskruntime "github.com/piper/piper/pkg/pipeline/worker/agent"
)

const testKubeconfig = "/Users/loykin/.kube/config"

func newTestLauncher(t *testing.T) *Launcher {
	t.Helper()
	if _, err := os.Stat(testKubeconfig); err != nil {
		t.Skipf("kubeconfig not found at %s: %v", testKubeconfig, err)
	}

	l, err := New(Config{
		Kubeconfig: testKubeconfig,
		InCluster:  false,
		AgentImage: "piper/agent:latest",
		Namespace:  "default",
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return l
}

// TestIntegration_ClusterConnectivity verifies cluster connectivity via kubeconfig.
func TestIntegration_ClusterConnectivity(t *testing.T) {
	l := newTestLauncher(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodes, err := l.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("cannot list nodes: %v", err)
	}
	t.Logf("cluster connected — %d node(s):", len(nodes.Items))
	for _, n := range nodes.Items {
		ready := "NotReady"
		for _, cond := range n.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = "Ready"
			}
		}
		t.Logf("  node %-30s %s", n.Name, ready)
	}
}

// TestIntegration_SimpleJob creates a busybox Job directly without agent injection
// and verifies the full K8s Job lifecycle (create → running → complete).
func TestIntegration_SimpleJob(t *testing.T) {
	l := newTestLauncher(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := fmt.Sprintf("simple-%d", time.Now().UnixNano())

	// registry:2: already pulled in K8s, Alpine-based → sh is available
	job := l.buildJob(
		&proto.Task{RunID: runID, StepName: "hello"},
		"registry:2",
		nil,
	)
	// Remove initContainer — run directly without agent injection
	job.Spec.Template.Spec.InitContainers = nil
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo hello-from-piper && sleep 1"}
	job.Spec.Template.Spec.Containers[0].Args = nil
	job.Spec.Template.Spec.Containers[0].ImagePullPolicy = "IfNotPresent"

	_, err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	jobID := job.Name
	t.Logf("job created: %s", jobID)
	defer func() {
		_ = l.clientset.BatchV1().Jobs(l.cfg.Namespace).Delete(ctx, jobID, metav1.DeleteOptions{})
	}()

	if err := waitForJob(ctx, t, l, jobID); err != nil {
		t.Fatal(err)
	}
}

// TestIntegration_DispatchJob runs a real K8s Job using the agent injection pattern
// and verifies that the agent calls back to a fake master server.
//
// Prerequisites:
//  1. The piper image must be pullable from K8s
//     - Docker Desktop: enable Settings → Experimental → "Use containerd for pulling and storing images", then run:
//     make docker IMAGE=piper/agent:latest
//     - Or push to a registry and set the PIPER_AGENT_IMAGE env var
func TestIntegration_DispatchJob(t *testing.T) {
	agentImage := os.Getenv("PIPER_AGENT_IMAGE")
	if agentImage == "" {
		t.Skip("PIPER_AGENT_IMAGE env var is not set (e.g. localhost:5000/piper/agent:latest)")
	}

	// ── Launcher ─────────────────────────────────────────────────────────────
	l := newTestLauncher(t)
	l.cfg.AgentImage = agentImage

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	runID := fmt.Sprintf("agent-%d", time.Now().UnixNano())
	stepName := "hello"

	step := pipeline.Step{
		Name: stepName,
		Run: pipeline.Run{
			Command: []string{"sh", "-c", "echo hello-from-piper-agent && sleep 1"},
		},
	}
	pl := pipeline.Pipeline{}

	stepJSON, _ := json.Marshal(step)
	plJSON, _ := json.Marshal(pl)
	task := &proto.Task{
		ID:       runID + ":" + stepName,
		RunID:    runID,
		StepName: stepName,
		Step:     stepJSON,
		Pipeline: plJSON,
	}

	args, err := taskruntime.BuildAgentExec(task, taskruntime.AgentExecConfig{
		OutputDir:  "/piper-outputs",
		InputDir:   "/piper-inputs",
		ResultFile: "/dev/termination-log",
	})
	if err != nil {
		t.Fatalf("build agent args: %v", err)
	}
	if _, err := l.CreateJob(ctx, task, "", "alpine:3.20", args, nil); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	jobID := jobName(task)
	t.Logf("job created: %s", jobID)
	defer func() {
		_ = l.clientset.BatchV1().Jobs(l.cfg.Namespace).Delete(ctx, jobID, metav1.DeleteOptions{})
	}()

	if err := waitForJob(ctx, t, l, jobID); err != nil {
		t.Fatal(err)
	}

	var got proto.TaskResult
	l.ReconcileJobs(ctx, func(_ context.Context, result proto.TaskResult) error {
		got = result
		return nil
	})
	if got.TaskID != task.ID || got.Status != proto.TaskStatusDone {
		t.Fatalf("termination result = %+v", got)
	}
}

func waitForJob(ctx context.Context, t *testing.T, l *Launcher, jobID string) error {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("job timed out")
		case <-time.After(2 * time.Second):
		}
		job, err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).Get(ctx, jobID, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		t.Logf("job status: active=%d succeeded=%d failed=%d",
			job.Status.Active, job.Status.Succeeded, job.Status.Failed)
		if job.Status.Succeeded > 0 {
			t.Logf("job succeeded")
			return nil
		}
		if job.Status.Failed > 0 {
			return fmt.Errorf("job failed")
		}
	}
}

func makeTaskID(runID, stepName string) string {
	return runID + ":" + stepName
}
