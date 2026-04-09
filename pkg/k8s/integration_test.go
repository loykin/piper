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
		MasterURL:    "",           // 테스트: master 없이 실행, 보고 스킵
		DefaultImage: "registry:2", // 이미 K8s에 pull된 이미지 사용
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return l
}

// TestIntegration_ClusterConnectivity는 kubeconfig로 클러스터 연결을 확인한다.
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

// TestIntegration_SimpleJob은 agent injection 없이 busybox Job을 직접 생성해
// K8s Job 생명주기(생성→실행→완료)를 검증한다.
func TestIntegration_SimpleJob(t *testing.T) {
	l := newTestLauncher(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := fmt.Sprintf("simple-%d", time.Now().UnixNano())

	// registry:2: 이미 K8s에 pull된 이미지, Alpine 기반 → sh 사용 가능
	job := l.buildJob(
		&proto.Task{RunID: runID, StepName: "hello"},
		"registry:2",
		nil,
	)
	// initContainer 제거 — agent injection 없이 직접 실행
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

// TestIntegration_DispatchJob은 agent injection 패턴으로 실제 K8s Job을 실행한다.
//
// 사전 요건:
//  1. piper/agent:latest 이미지가 K8s에서 pull 가능해야 함
//     - Docker Desktop: Settings → Experimental → "Use containerd for pulling and storing images" 활성화 후
//     docker build -t piper/agent:latest -f Dockerfile.agent .
//     - 또는 로컬 레지스트리에 push 후 PIPER_AGENT_IMAGE 환경변수 설정
func TestIntegration_DispatchJob(t *testing.T) {
	agentImage := os.Getenv("PIPER_AGENT_IMAGE")
	if agentImage == "" {
		t.Skip("PIPER_AGENT_IMAGE 환경변수가 설정되지 않음 (예: localhost:5000/piper/agent:latest)")
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
