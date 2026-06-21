package servingdispatch

import (
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/serving"
)

// applyPodPolicy merges workerPolicy (base) into the serving YAML's pod_template.
// The manifest's own pod_template takes precedence on any conflict.
func applyPodPolicy(yamlStr string, policy corev1.PodTemplateSpec) (string, error) {
	var ms serving.ModelService
	if err := yaml.Unmarshal([]byte(yamlStr), &ms); err != nil {
		return yamlStr, err
	}
	if ms.Spec.Driver.K8s == nil {
		return yamlStr, nil
	}

	ms.Spec.Driver.K8s.PodTemplate = iagent.MergePodTemplate(policy, ms.Spec.Driver.K8s.PodTemplate)

	out, err := yaml.Marshal(&ms)
	if err != nil {
		return yamlStr, err
	}
	return string(out), nil
}
