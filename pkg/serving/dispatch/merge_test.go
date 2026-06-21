package servingdispatch

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestApplyPodPolicy_InvalidYAML(t *testing.T) {
	_, err := applyPodPolicy("not: valid: yaml: [", corev1.PodTemplateSpec{})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestApplyPodPolicy_NonK8sYAML(t *testing.T) {
	yaml := `apiVersion: piper.dev/v1
kind: ModelService
metadata:
  name: test
spec:
  driver:
    placement:
      runtime: docker
    docker:
      image: mymodel:latest
  run:
    command: ["python", "serve.py"]
`
	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"gpu": "true"}

	out, err := applyPodPolicy(yaml, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "nodeSelector") {
		t.Fatal("policy should not be injected into non-K8s model service")
	}
}

func TestApplyPodPolicy_K8sYAML(t *testing.T) {
	yaml := `apiVersion: piper.dev/v1
kind: ModelService
metadata:
  name: gpu-model
spec:
  driver:
    placement:
      runtime: k8s
    k8s:
      image: mymodel:latest
      namespace: ml
  run:
    command: ["python", "serve.py"]
`
	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"nvidia.com/gpu": "true"}
	policy.Spec.Tolerations = []corev1.Toleration{{Key: "dedicated", Value: "gpu"}}

	out, err := applyPodPolicy(yaml, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "nvidia.com/gpu") {
		t.Error("merged YAML should contain policy nodeSelector")
	}
	if !strings.Contains(out, "dedicated") {
		t.Error("merged YAML should contain policy toleration")
	}
}

func TestApplyPodPolicy_ManifestWinsOnConflict(t *testing.T) {
	yaml := `apiVersion: piper.dev/v1
kind: ModelService
metadata:
  name: custom-model
spec:
  driver:
    placement:
      runtime: k8s
    k8s:
      image: mymodel:latest
      namespace: ml
      pod_template:
        spec:
          nodeSelector:
            nvidia.com/gpu: "false"
  run:
    command: ["python", "serve.py"]
`
	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"nvidia.com/gpu": "true"}

	out, err := applyPodPolicy(yaml, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `"true"`) && !strings.Contains(out, `"false"`) {
		t.Error("manifest pod_template should win over policy on conflict (expected false, not true)")
	}
}

func TestApplyPodPolicy_EmptyPolicyPreservesManifest(t *testing.T) {
	yaml := `apiVersion: piper.dev/v1
kind: ModelService
metadata:
  name: existing
spec:
  driver:
    placement:
      runtime: k8s
    k8s:
      image: mymodel:latest
      namespace: ml
      pod_template:
        spec:
          nodeSelector:
            zone: us-east-1
  run:
    command: ["python", "serve.py"]
`
	out, err := applyPodPolicy(yaml, corev1.PodTemplateSpec{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "us-east-1") {
		t.Error("empty policy should preserve manifest nodeSelector")
	}
}
