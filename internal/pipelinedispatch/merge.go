package pipelinedispatch

import (
	corev1 "k8s.io/api/core/v1"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/pkg/pipeline"
)

// applyPodPolicyToPipelineYAML merges workerPolicy (base) into all K8s
// driver pod_templates in the pipeline YAML (defaults + per-step). The
// step's own pod_template takes precedence on any field conflict.
func applyPodPolicyToPipelineYAML(pipelineYAML string, policy corev1.PodTemplateSpec) (string, error) {
	return iagent.ApplyPodPolicyYAML[pipeline.Pipeline](pipelineYAML, policy, applyPolicyToPipeline)
}

func applyPolicyToPipeline(pl *pipeline.Pipeline, policy corev1.PodTemplateSpec) bool {
	changed := false
	if pl.Spec.Defaults != nil && pl.Spec.Defaults.Driver.K8s != nil {
		pl.Spec.Defaults.Driver.K8s.PodTemplate = iagent.MergePodTemplate(policy, pl.Spec.Defaults.Driver.K8s.PodTemplate)
		changed = true
	}
	for i := range pl.Spec.Steps {
		if pl.Spec.Steps[i].Driver.K8s == nil {
			continue
		}
		pl.Spec.Steps[i].Driver.K8s.PodTemplate = iagent.MergePodTemplate(policy, pl.Spec.Steps[i].Driver.K8s.PodTemplate)
		changed = true
	}
	return changed
}
