package pipelinedispatch

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/pipeline"
)

// applyPodPolicyToPipeline merges workerPolicy (base) into all K8s driver
// pod_templates in the pipeline JSON (defaults + per-step). The step's own
// pod_template takes precedence on any field conflict.
func applyPodPolicyToPipeline(pipelineJSON []byte, policy corev1.PodTemplateSpec) ([]byte, error) {
	var pl pipeline.Pipeline
	if err := json.Unmarshal(pipelineJSON, &pl); err != nil {
		return pipelineJSON, err
	}

	if pl.Spec.Defaults != nil && pl.Spec.Defaults.Driver.K8s != nil {
		pl.Spec.Defaults.Driver.K8s.PodTemplate = iagent.MergePodTemplate(
			policy, pl.Spec.Defaults.Driver.K8s.PodTemplate,
		)
	}

	for i := range pl.Spec.Steps {
		if pl.Spec.Steps[i].Driver.K8s != nil {
			pl.Spec.Steps[i].Driver.K8s.PodTemplate = iagent.MergePodTemplate(
				policy, pl.Spec.Steps[i].Driver.K8s.PodTemplate,
			)
		}
	}

	return json.Marshal(&pl)
}
