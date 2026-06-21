package notebookdispatch

import (
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/notebook"
)

// applyPodPolicy merges workerPolicy (base) into the notebook YAML's pod_template.
// The manifest's own pod_template takes precedence on any conflict.
func applyPodPolicy(yamlStr string, policy corev1.PodTemplateSpec) (string, error) {
	var nb notebook.Notebook
	if err := yaml.Unmarshal([]byte(yamlStr), &nb); err != nil {
		return yamlStr, err
	}
	if nb.Spec.Driver.K8s == nil {
		return yamlStr, nil
	}

	nb.Spec.Driver.K8s.PodTemplate = iagent.MergePodTemplate(policy, nb.Spec.Driver.K8s.PodTemplate)

	out, err := yaml.Marshal(&nb)
	if err != nil {
		return yamlStr, err
	}
	return string(out), nil
}
