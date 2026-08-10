package pipelinedispatch

import (
	"testing"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"

	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
)

func TestApplyPodPolicyToPipelineYAML_InvalidYAML(t *testing.T) {
	_, err := applyPodPolicyToPipelineYAML("not: [valid", corev1.PodTemplateSpec{})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestApplyPodPolicyToPipelineYAML_NoK8sDriver(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Steps = []pipeline.Step{{Name: "step1"}}
	data, _ := yaml.Marshal(&pl)

	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"gpu": "true"}

	result, err := applyPodPolicyToPipelineYAML(string(data), policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got pipeline.Pipeline
	if err := yaml.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Spec.Defaults != nil && got.Spec.Defaults.Driver.K8s != nil {
		if len(got.Spec.Defaults.Driver.K8s.PodTemplate.Spec.NodeSelector) > 0 {
			t.Fatal("policy should not be applied to non-K8s pipeline")
		}
	}
	for _, s := range got.Spec.Steps {
		if s.Driver.K8s != nil && len(s.Driver.K8s.PodTemplate.Spec.NodeSelector) > 0 {
			t.Fatal("policy should not be applied to non-K8s step")
		}
	}
}

func TestApplyPodPolicyToPipelineYAML_DefaultsK8sMerged(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{Image: "train:latest"},
		},
	}
	data, _ := yaml.Marshal(&pl)

	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"gpu": "true"}
	policy.Spec.Tolerations = []corev1.Toleration{{Key: "dedicated", Value: "gpu"}}

	result, err := applyPodPolicyToPipelineYAML(string(data), policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got pipeline.Pipeline
	if err := yaml.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Spec.Defaults == nil || got.Spec.Defaults.Driver.K8s == nil {
		t.Fatal("defaults K8s driver should be present")
	}
	pt := got.Spec.Defaults.Driver.K8s.PodTemplate
	if pt.Spec.NodeSelector["gpu"] != "true" {
		t.Errorf("policy nodeSelector should be applied to defaults: got %v", pt.Spec.NodeSelector)
	}
	if len(pt.Spec.Tolerations) == 0 {
		t.Error("policy tolerations should be applied to defaults")
	}
}

func TestApplyPodPolicyToPipelineYAML_StepK8sMerged(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Steps = []pipeline.Step{
		{
			Name:   "train",
			Driver: manifest.DriverSpec{K8s: &manifest.DriverK8sSpec{Image: "step:latest"}},
		},
	}
	data, _ := yaml.Marshal(&pl)

	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"region": "us-east-1"}

	result, err := applyPodPolicyToPipelineYAML(string(data), policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got pipeline.Pipeline
	if err := yaml.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(got.Spec.Steps) != 1 || got.Spec.Steps[0].Driver.K8s == nil {
		t.Fatal("step K8s driver should be present")
	}
	ns := got.Spec.Steps[0].Driver.K8s.PodTemplate.Spec.NodeSelector
	if ns["region"] != "us-east-1" {
		t.Errorf("policy nodeSelector should be applied to step: got %v", ns)
	}
}

func TestApplyPodPolicyToPipelineYAML_ManifestWinsOnConflict(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{
				Image: "train:latest",
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						NodeSelector: map[string]string{"tier": "manifest"},
					},
				},
			},
		},
	}
	data, _ := yaml.Marshal(&pl)

	policy := corev1.PodTemplateSpec{}
	policy.Spec.NodeSelector = map[string]string{"tier": "policy", "gpu": "true"}

	result, err := applyPodPolicyToPipelineYAML(string(data), policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got pipeline.Pipeline
	if err := yaml.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	ns := got.Spec.Defaults.Driver.K8s.PodTemplate.Spec.NodeSelector
	if ns["tier"] != "manifest" {
		t.Errorf("manifest should win on conflict: tier=%q (want manifest)", ns["tier"])
	}
	if ns["gpu"] != "true" {
		t.Errorf("policy should fill gap: gpu=%q (want true)", ns["gpu"])
	}
}

func TestApplyPodPolicyToPipelineYAML_EmptyPolicyIsNoOp(t *testing.T) {
	pl := pipeline.Pipeline{}
	pl.Spec.Defaults = &pipeline.PipelineDefaults{
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{
				Image: "train:latest",
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						NodeSelector: map[string]string{"existing": "value"},
					},
				},
			},
		},
	}
	data, _ := yaml.Marshal(&pl)

	result, err := applyPodPolicyToPipelineYAML(string(data), corev1.PodTemplateSpec{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got pipeline.Pipeline
	if err := yaml.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	ns := got.Spec.Defaults.Driver.K8s.PodTemplate.Spec.NodeSelector
	if ns["existing"] != "value" {
		t.Errorf("empty policy should preserve manifest nodeSelector: got %v", ns)
	}
}
