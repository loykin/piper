package notebookdispatch

import (
	corev1 "k8s.io/api/core/v1"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/pkg/notebook"
)

// applyPodPolicy merges workerPolicy (base) into the notebook YAML's pod_template.
// The manifest's own pod_template takes precedence on any conflict.
func applyPodPolicy(yamlStr string, policy corev1.PodTemplateSpec) (string, error) {
	return iagent.ApplyPodPolicyYAML[notebook.Notebook](yamlStr, policy, applyPolicyToNotebook)
}

func applyPolicyToNotebook(nb *notebook.Notebook, policy corev1.PodTemplateSpec) bool {
	if nb.Spec.Driver.K8s == nil {
		return false
	}
	nb.Spec.Driver.K8s.PodTemplate = iagent.MergePodTemplate(policy, nb.Spec.Driver.K8s.PodTemplate)
	return true
}
