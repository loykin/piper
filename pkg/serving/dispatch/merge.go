package servingdispatch

import (
	corev1 "k8s.io/api/core/v1"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/serving"
)

// applyPodPolicy merges workerPolicy (base) into the serving YAML's pod_template.
// The manifest's own pod_template takes precedence on any conflict.
func applyPodPolicy(yamlStr string, policy corev1.PodTemplateSpec) (string, error) {
	return iagent.ApplyPodPolicyYAML[serving.ModelService](yamlStr, policy, applyPolicyToModelService)
}

func applyPolicyToModelService(ms *serving.ModelService, policy corev1.PodTemplateSpec) bool {
	if ms.Spec.Driver.K8s == nil {
		return false
	}
	ms.Spec.Driver.K8s.PodTemplate = iagent.MergePodTemplate(policy, ms.Spec.Driver.K8s.PodTemplate)
	return true
}
