package piper

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/pkg/project"
)

func TestConfigValidateK8sRuntime(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.AllowInsecureDevKey = true
	cfg.Storage.Disabled = true
	cfg.Runtime = RuntimeConfig{Type: RuntimeK8s, K8s: K8sRuntimeConfig{
		Client: fake.NewSimpleClientset(), Namespaces: []string{"runs"},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSettingsExposeServerOwnedRuntime(t *testing.T) {
	p := &Piper{cfg: Config{Runtime: RuntimeConfig{Type: RuntimeK8s}, Storage: StorageConfig{Disabled: true}}}
	if got := p.Settings().Runtime.Type; got != RuntimeK8s {
		t.Fatalf("Settings().Runtime.Type = %q, want %q", got, RuntimeK8s)
	}
}

func TestConfigValidateK8sRuntimeRequiresWorkloadURLForFileStore(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.AllowInsecureDevKey = true
	cfg.Runtime = RuntimeConfig{Type: RuntimeK8s, K8s: K8sRuntimeConfig{
		Client: fake.NewSimpleClientset(), Namespaces: []string{"runs"},
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "workload_url is required") {
		t.Fatalf("Validate() error = %v, want workload_url requirement", err)
	}
}

func TestPiperK8sRuntimeDispatchesWithoutRegisteredPipelineWorker(t *testing.T) {
	client := fake.NewSimpleClientset()
	cfg := DefaultConfig()
	cfg.OutputDir = t.TempDir()
	cfg.Server.AllowInsecureDevKey = true
	cfg.Storage.Disabled = true
	cfg.Runtime = RuntimeConfig{Type: RuntimeK8s, K8s: K8sRuntimeConfig{
		Client: client, Namespaces: []string{"runs"}, PipelineRunnerImage: "piper:test",
	}}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	yaml := `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: direct-k8s
spec:
  steps:
    - name: run
      driver:
        placement:
          runtime: k8s
        k8s:
          image: alpine:3.20
          namespace: runs
      run:
        type: command
        command: [sh, -c, echo ok]
`
	ctx := project.WithContext(context.Background(), project.Context{ID: project.DefaultID})
	runID, err := p.runs.StartRunFromAPI(ctx, yaml, nil, BuiltinVars{}, "")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		jobs, listErr := client.BatchV1().Jobs("runs").List(ctx, metav1.ListOptions{})
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(jobs.Items) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("direct runtime did not create a Job for run %s", runID)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := p.queue.Cancel(ctx, project.DefaultID, runID); err != nil {
		t.Fatal(err)
	}
	jobs, err := client.BatchV1().Jobs("runs").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("jobs after queue cancellation = %d, want 0", len(jobs.Items))
	}
}
