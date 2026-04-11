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

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

const testKubeconfig = "/Users/loykin/.kube/config"

func newTestLauncher(t *testing.T) *Launcher {
	t.Helper()
	if _, err := os.Stat(testKubeconfig); err != nil {
		t.Skipf("kubeconfig not found at %s: %v", testKubeconfig, err)
	}

	l, err := New(Config{
		Kubeconfig:   testKubeconfig,
		InCluster:    false,
		AgentImage:   "piper/agent:latest",
		Namespace:    "default",
		MasterURL:    "",           // Test: run without master, skip reporting
		DefaultImage: "registry:2", // Use an image already pulled in K8s
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

// TestIntegration_DispatchJob runs a real K8s Job using the agent injection pattern.
//
// Prerequisites:
//  1. The piper/agent:latest image must be pullable from K8s
//     - Docker Desktop: enable Settings → Experimental → "Use containerd for pulling and storing images", then run:
//     docker build -t piper/agent:latest -f Dockerfile.agent .
//     - Or push to a local registry and set the PIPER_AGENT_IMAGE env var
func TestIntegration_DispatchJob(t *testing.T) {
	agentImage := os.Getenv("PIPER_AGENT_IMAGE")
	if agentImage == "" {
		t.Skip("PIPER_AGENT_IMAGE env var is not set (e.g. localhost:5000/piper/agent:latest)")
	}

	l := newTestLauncher(t)
	l.cfg.AgentImage = agentImage

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	runID := fmt.Sprintf("agent-%d", time.Now().UnixNano())
	stepName := "hello"

	step := pipeline.Step{
		Name: stepName,
		Run: pipeline.Run{
			Image:   "busybox:latest",
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

	if err := l.Dispatch(ctx, task); err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	jobID := jobName(task)
	t.Logf("job created: %s", jobID)
	defer func() {
		_ = l.clientset.BatchV1().Jobs(l.cfg.Namespace).Delete(ctx, jobID, metav1.DeleteOptions{})
	}()

	if err := waitForJob(ctx, t, l, jobID); err != nil {
		t.Fatal(err)
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
