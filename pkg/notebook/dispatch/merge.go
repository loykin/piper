package notebookdispatch

import (
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"

	"github.com/piper/piper/pkg/notebook"
)

// applyPodPolicy merges workerPolicy (base) into the notebook YAML's pod_template.
// The manifest's own pod_template takes precedence on any conflict.
// Returns the original yamlStr unchanged on any parse/serialise error.
func applyPodPolicy(yamlStr string, policy corev1.PodTemplateSpec) (string, error) {
	var nb notebook.Notebook
	if err := yaml.Unmarshal([]byte(yamlStr), &nb); err != nil {
		return yamlStr, err
	}
	if nb.Spec.Driver.K8s == nil {
		return yamlStr, nil
	}

	nb.Spec.Driver.K8s.PodTemplate = mergePodTemplate(policy, nb.Spec.Driver.K8s.PodTemplate)

	out, err := yaml.Marshal(&nb)
	if err != nil {
		return yamlStr, err
	}
	return string(out), nil
}

// mergePodTemplate merges base and overlay PodTemplateSpecs.
// overlay wins on any field conflict; base fills in gaps.
func mergePodTemplate(base, overlay corev1.PodTemplateSpec) corev1.PodTemplateSpec {
	result := *base.DeepCopy()

	result.Labels = mergeStringMaps(result.Labels, overlay.Labels)
	result.Annotations = mergeStringMaps(result.Annotations, overlay.Annotations)

	result.Spec.NodeSelector = mergeStringMaps(result.Spec.NodeSelector, overlay.Spec.NodeSelector)
	result.Spec.Tolerations = mergeTolerations(result.Spec.Tolerations, overlay.Spec.Tolerations)
	result.Spec.Volumes = mergeVolumes(result.Spec.Volumes, overlay.Spec.Volumes)
	result.Spec.InitContainers = mergeContainerSlice(result.Spec.InitContainers, overlay.Spec.InitContainers)
	result.Spec.Containers = mergeContainerSlice(result.Spec.Containers, overlay.Spec.Containers)

	if overlay.Spec.RuntimeClassName != nil {
		result.Spec.RuntimeClassName = overlay.Spec.RuntimeClassName
	}
	if overlay.Spec.ServiceAccountName != "" {
		result.Spec.ServiceAccountName = overlay.Spec.ServiceAccountName
	}
	if overlay.Spec.SecurityContext != nil {
		result.Spec.SecurityContext = overlay.Spec.SecurityContext
	}
	if overlay.Spec.PriorityClassName != "" {
		result.Spec.PriorityClassName = overlay.Spec.PriorityClassName
	}
	if overlay.Spec.HostNetwork {
		result.Spec.HostNetwork = true
	}

	return result
}

func mergeStringMaps(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	result := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		result[k] = v
	}
	return result
}

// mergeTolerations unions base and overlay. Overlay items are taken first;
// base items with keys not already present in overlay are appended.
func mergeTolerations(base, overlay []corev1.Toleration) []corev1.Toleration {
	if len(overlay) == 0 {
		return base
	}
	result := make([]corev1.Toleration, len(overlay))
	copy(result, overlay)
	overlayKeys := make(map[string]struct{}, len(overlay))
	for _, t := range overlay {
		overlayKeys[t.Key] = struct{}{}
	}
	for _, t := range base {
		if _, found := overlayKeys[t.Key]; !found {
			result = append(result, t)
		}
	}
	return result
}

// mergeVolumes unions base and overlay by name. Overlay wins on name conflict.
func mergeVolumes(base, overlay []corev1.Volume) []corev1.Volume {
	if len(overlay) == 0 {
		return base
	}
	overlayNames := make(map[string]struct{}, len(overlay))
	for _, v := range overlay {
		overlayNames[v.Name] = struct{}{}
	}
	result := make([]corev1.Volume, 0, len(base)+len(overlay))
	for _, v := range base {
		if _, found := overlayNames[v.Name]; !found {
			result = append(result, v)
		}
	}
	result = append(result, overlay...)
	return result
}

// mergeContainerSlice unions base and overlay by container name. Overlay wins on name conflict.
func mergeContainerSlice(base, overlay []corev1.Container) []corev1.Container {
	if len(overlay) == 0 {
		return base
	}
	overlayNames := make(map[string]struct{}, len(overlay))
	for _, c := range overlay {
		overlayNames[c.Name] = struct{}{}
	}
	result := make([]corev1.Container, 0, len(base)+len(overlay))
	for _, c := range base {
		if _, found := overlayNames[c.Name]; !found {
			result = append(result, c)
		}
	}
	result = append(result, overlay...)
	return result
}
