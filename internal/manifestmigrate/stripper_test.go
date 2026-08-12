package manifestmigrate

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStripPlacementWorkerLabel_NotebookSingleDriver(t *testing.T) {
	in := `apiVersion: piper/v1
kind: Notebook
metadata:
  name: research
spec:
  driver:
    placement:
      worker: gpu-server-1
      runtime: baremetal
    process:
      env: conda:ml-env
`
	out, changed, err := StripPlacementWorkerLabel(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if strings.Contains(out, "worker:") {
		t.Fatalf("worker key still present:\n%s", out)
	}
	if !strings.Contains(out, "runtime: baremetal") {
		t.Fatalf("runtime key should survive:\n%s", out)
	}
	if !strings.Contains(out, "env: conda:ml-env") {
		t.Fatalf("process.env should survive:\n%s", out)
	}
	assertValidYAML(t, out)
}

func TestStripPlacementWorkerLabel_PipelineMultipleDriverBlocks(t *testing.T) {
	in := `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: training
spec:
  defaults:
    driver:
      placement:
        runtime: k8s
        label: gpu
  steps:
    - name: extract
      run:
        command: ["echo", "hi"]
    - name: train
      driver:
        placement:
          worker: gpu-node-1
        k8s:
          image: python:3.12-slim
`
	out, changed, err := StripPlacementWorkerLabel(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if strings.Contains(out, "worker:") || strings.Contains(out, "label:") {
		t.Fatalf("worker/label keys still present:\n%s", out)
	}
	if !strings.Contains(out, "runtime: k8s") {
		t.Fatalf("defaults.driver.placement.runtime should survive:\n%s", out)
	}
	if !strings.Contains(out, "image: python:3.12-slim") {
		t.Fatalf("step driver.k8s.image should survive:\n%s", out)
	}
	assertValidYAML(t, out)
}

func TestStripPlacementWorkerLabel_NoPlacementFields_Unchanged(t *testing.T) {
	in := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: svc
spec:
  driver:
    placement:
      runtime: docker
    docker:
      image: my/model:latest
`
	out, changed, err := StripPlacementWorkerLabel(in)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("expected changed=false, got output:\n%s", out)
	}
	if out != in {
		t.Fatalf("expected identical passthrough, got:\n%s", out)
	}
}

func TestStripPlacementWorkerLabel_NoDriverBlock_Unchanged(t *testing.T) {
	in := `apiVersion: piper/v1
kind: Notebook
metadata:
  name: research
spec:
  volume:
    size: 20Gi
`
	out, changed, err := StripPlacementWorkerLabel(in)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("expected changed=false, got output:\n%s", out)
	}
	if out != in {
		t.Fatalf("expected identical passthrough, got:\n%s", out)
	}
}

func TestStripPlacementWorkerLabel_InvalidYAML(t *testing.T) {
	if _, _, err := StripPlacementWorkerLabel("not: [valid"); err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

func assertValidYAML(t *testing.T, text string) {
	t.Helper()
	var out any
	if err := yaml.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("output is not valid yaml: %v\n%s", err, text)
	}
}
