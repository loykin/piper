package notebookdispatch

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// --- mergePodTemplate ---

func TestMergePodTemplate_EmptyPolicy(t *testing.T) {
	var policy corev1.PodTemplateSpec
	overlay := corev1.PodTemplateSpec{}
	overlay.Spec.NodeSelector = map[string]string{"gpu": "true"}

	result := mergePodTemplate(policy, overlay)
	if result.Spec.NodeSelector["gpu"] != "true" {
		t.Fatalf("expected node selector from overlay, got %v", result.Spec.NodeSelector)
	}
}

func TestMergePodTemplate_EmptyOverlay(t *testing.T) {
	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"gpu": "true"}

	result := mergePodTemplate(policy, corev1.PodTemplateSpec{})
	if result.Spec.NodeSelector["gpu"] != "true" {
		t.Fatalf("expected node selector from policy, got %v", result.Spec.NodeSelector)
	}
}

func TestMergePodTemplate_NodeSelectorOverlayWins(t *testing.T) {
	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"tier": "base", "gpu": "a10"}

	overlay := corev1.PodTemplateSpec{}
	overlay.Spec.NodeSelector = map[string]string{"gpu": "a100", "zone": "us-east-1"}

	result := mergePodTemplate(policy, overlay)
	// overlay wins on conflict
	if result.Spec.NodeSelector["gpu"] != "a100" {
		t.Fatalf("overlay should win gpu, got %q", result.Spec.NodeSelector["gpu"])
	}
	// base fills gaps
	if result.Spec.NodeSelector["tier"] != "base" {
		t.Fatalf("policy tier should be preserved, got %q", result.Spec.NodeSelector["tier"])
	}
	// overlay adds new keys
	if result.Spec.NodeSelector["zone"] != "us-east-1" {
		t.Fatalf("overlay zone should be present, got %q", result.Spec.NodeSelector["zone"])
	}
}

func TestMergePodTemplate_TolerationsUnion(t *testing.T) {
	policy := corev1.PodTemplateSpec{}
	policy.Spec.Tolerations = []corev1.Toleration{
		{Key: "dedicated", Value: "gpu"},
		{Key: "spot"},
	}

	overlay := corev1.PodTemplateSpec{}
	overlay.Spec.Tolerations = []corev1.Toleration{
		{Key: "dedicated", Value: "gpu-premium"}, // same key — overlay wins
	}

	result := mergePodTemplate(policy, overlay)
	if len(result.Spec.Tolerations) != 2 {
		t.Fatalf("expected 2 tolerations (union), got %d", len(result.Spec.Tolerations))
	}
	// overlay's dedicated takes precedence
	if result.Spec.Tolerations[0].Value != "gpu-premium" {
		t.Fatalf("overlay toleration should win, got %q", result.Spec.Tolerations[0].Value)
	}
	// policy's spot key not in overlay — added
	found := false
	for _, tol := range result.Spec.Tolerations {
		if tol.Key == "spot" {
			found = true
		}
	}
	if !found {
		t.Fatal("policy toleration with key=spot should be present in result")
	}
}

func TestMergePodTemplate_VolumesOverlayWins(t *testing.T) {
	policy := corev1.PodTemplateSpec{}
	policy.Spec.Volumes = []corev1.Volume{
		{Name: "fuse", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}

	overlay := corev1.PodTemplateSpec{}
	overlay.Spec.Volumes = []corev1.Volume{
		{Name: "data", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/mnt/data"}}},
	}

	result := mergePodTemplate(policy, overlay)
	if len(result.Spec.Volumes) != 2 {
		t.Fatalf("expected 2 volumes (union), got %d", len(result.Spec.Volumes))
	}
	// overlay's data wins
	for _, v := range result.Spec.Volumes {
		if v.Name == "data" && v.HostPath == nil {
			t.Fatal("overlay volume 'data' should use HostPath from overlay")
		}
	}
	// policy's fuse is preserved
	found := false
	for _, v := range result.Spec.Volumes {
		if v.Name == "fuse" {
			found = true
		}
	}
	if !found {
		t.Fatal("policy volume 'fuse' should be in result")
	}
}

func TestMergePodTemplate_RuntimeClassOverlay(t *testing.T) {
	name := "nvidia"
	overlay := corev1.PodTemplateSpec{}
	overlay.Spec.RuntimeClassName = &name

	result := mergePodTemplate(corev1.PodTemplateSpec{}, overlay)
	if result.Spec.RuntimeClassName == nil || *result.Spec.RuntimeClassName != "nvidia" {
		t.Fatalf("expected runtimeClassName=nvidia from overlay")
	}
}

func TestMergePodTemplate_PolicyRuntimeClassNotOverriddenWhenOverlayEmpty(t *testing.T) {
	name := "nvidia"
	policy := corev1.PodTemplateSpec{}
	policy.Spec.RuntimeClassName = &name

	result := mergePodTemplate(policy, corev1.PodTemplateSpec{})
	if result.Spec.RuntimeClassName == nil || *result.Spec.RuntimeClassName != "nvidia" {
		t.Fatalf("expected runtimeClassName from policy when overlay is empty")
	}
}

// --- applyPodPolicy ---

func TestApplyPodPolicy_NonK8sYAML(t *testing.T) {
	yaml := `apiVersion: piper.dev/v1
kind: Notebook
metadata:
  name: test
spec:
  driver:
    placement:
      runtime: docker
    docker:
      image: jupyter/base:latest
  notebook:
    image: jupyter/base:latest
`
	var policy corev1.PodTemplateSpec
	policy.Spec.NodeSelector = map[string]string{"gpu": "true"}

	out, err := applyPodPolicy(yaml, policy)
	if err != nil {
		t.Fatalf("applyPodPolicy error: %v", err)
	}
	// docker notebook — no K8s spec, policy must not be injected
	if strings.Contains(out, "nodeSelector") {
		t.Fatalf("nodeSelector should not appear in docker notebook YAML")
	}
}

func TestApplyPodPolicy_K8sYAML(t *testing.T) {
	yaml := `apiVersion: piper.dev/v1
kind: Notebook
metadata:
  name: gpu-nb
spec:
  driver:
    placement:
      runtime: k8s
    k8s:
      image: jupyter/scipy-notebook:latest
      namespace: ml
  notebook:
    image: jupyter/scipy-notebook:latest
  volume:
    size: 10Gi
`
	var policy corev1.PodTemplateSpec
	policy.Spec.NodeSelector = map[string]string{"nvidia.com/gpu": "true"}
	policy.Spec.Tolerations = []corev1.Toleration{{Key: "dedicated", Value: "gpu"}}

	out, err := applyPodPolicy(yaml, policy)
	if err != nil {
		t.Fatalf("applyPodPolicy error: %v", err)
	}
	if !strings.Contains(out, "nvidia.com/gpu") {
		t.Errorf("merged YAML should contain policy nodeSelector")
	}
	if !strings.Contains(out, "dedicated") {
		t.Errorf("merged YAML should contain policy toleration")
	}
}

func TestApplyPodPolicy_ManifestWinsOnConflict(t *testing.T) {
	yaml := `apiVersion: piper.dev/v1
kind: Notebook
metadata:
  name: custom-nb
spec:
  driver:
    placement:
      runtime: k8s
    k8s:
      image: jupyter/scipy-notebook:latest
      namespace: ml
      pod_template:
        spec:
          nodeSelector:
            nvidia.com/gpu: "false"
  notebook:
    image: jupyter/scipy-notebook:latest
  volume:
    size: 10Gi
`
	var policy corev1.PodTemplateSpec
	policy.Spec.NodeSelector = map[string]string{"nvidia.com/gpu": "true"}

	out, err := applyPodPolicy(yaml, policy)
	if err != nil {
		t.Fatalf("applyPodPolicy error: %v", err)
	}
	if strings.Contains(out, `"true"`) && !strings.Contains(out, `"false"`) {
		t.Errorf("manifest pod_template should win over policy (expected false, got true)")
	}
}

func TestApplyPodPolicy_InvalidYAML(t *testing.T) {
	_, err := applyPodPolicy("not: valid: yaml: [", corev1.PodTemplateSpec{})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// --- merge helpers ---

func TestMergeStringMaps_BothEmpty(t *testing.T) {
	result := mergeStringMaps(nil, nil)
	if result != nil {
		t.Fatalf("expected nil for both-empty merge, got %v", result)
	}
}

func TestMergeTolerations_BothEmpty(t *testing.T) {
	result := mergeTolerations(nil, nil)
	if len(result) != 0 {
		t.Fatalf("expected empty, got %v", result)
	}
}

func TestMergeVolumes_DeduplicatesByName(t *testing.T) {
	base := []corev1.Volume{{Name: "a"}, {Name: "b"}}
	overlay := []corev1.Volume{{Name: "b"}, {Name: "c"}}
	result := mergeVolumes(base, overlay)
	if len(result) != 3 {
		t.Fatalf("expected 3 volumes (a,b,c), got %d: %v", len(result), result)
	}
}
