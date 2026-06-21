package agent

import corev1 "k8s.io/api/core/v1"

// MergePodTemplate merges base and overlay PodTemplateSpecs.
// overlay wins on any field conflict; base fills in gaps.
//
// Boolean fields (HostNetwork, HostPID, HostIPC) always take the overlay's
// value because Go's zero value (false) is indistinguishable from "not set".
// Consequently, a policy that sets HostNetwork:true cannot be preserved when
// the manifest omits HostNetwork (its zero value is false, which wins).
func MergePodTemplate(base, overlay corev1.PodTemplateSpec) corev1.PodTemplateSpec {
	result := *base.DeepCopy()

	result.Labels = MergeStringMaps(result.Labels, overlay.Labels)
	result.Annotations = MergeStringMaps(result.Annotations, overlay.Annotations)

	result.Spec.NodeSelector = MergeStringMaps(result.Spec.NodeSelector, overlay.Spec.NodeSelector)
	result.Spec.Tolerations = MergeTolerations(result.Spec.Tolerations, overlay.Spec.Tolerations)
	result.Spec.Volumes = MergeVolumes(result.Spec.Volumes, overlay.Spec.Volumes)
	result.Spec.InitContainers = MergeContainerSlice(result.Spec.InitContainers, overlay.Spec.InitContainers)
	result.Spec.Containers = MergeContainerSlice(result.Spec.Containers, overlay.Spec.Containers)
	result.Spec.ImagePullSecrets = MergeImagePullSecrets(result.Spec.ImagePullSecrets, overlay.Spec.ImagePullSecrets)
	result.Spec.TopologySpreadConstraints = MergeTopologySpreadConstraints(
		result.Spec.TopologySpreadConstraints, overlay.Spec.TopologySpreadConstraints,
	)

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
	if overlay.Spec.Affinity != nil {
		result.Spec.Affinity = overlay.Spec.Affinity
	}
	if overlay.Spec.DNSConfig != nil {
		result.Spec.DNSConfig = overlay.Spec.DNSConfig
	}
	if overlay.Spec.SchedulerName != "" {
		result.Spec.SchedulerName = overlay.Spec.SchedulerName
	}
	// Booleans: overlay always wins (false is indistinguishable from "not set").
	result.Spec.HostNetwork = overlay.Spec.HostNetwork
	result.Spec.HostPID = overlay.Spec.HostPID
	result.Spec.HostIPC = overlay.Spec.HostIPC

	return result
}

func MergeStringMaps(base, overlay map[string]string) map[string]string {
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

func MergeTolerations(base, overlay []corev1.Toleration) []corev1.Toleration {
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

func MergeVolumes(base, overlay []corev1.Volume) []corev1.Volume {
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

func MergeContainerSlice(base, overlay []corev1.Container) []corev1.Container {
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

// MergeImagePullSecrets unions base and overlay by secret name; overlay wins on conflict.
func MergeImagePullSecrets(base, overlay []corev1.LocalObjectReference) []corev1.LocalObjectReference {
	if len(overlay) == 0 {
		return base
	}
	overlayNames := make(map[string]struct{}, len(overlay))
	for _, s := range overlay {
		overlayNames[s.Name] = struct{}{}
	}
	result := make([]corev1.LocalObjectReference, 0, len(base)+len(overlay))
	for _, s := range base {
		if _, found := overlayNames[s.Name]; !found {
			result = append(result, s)
		}
	}
	return append(result, overlay...)
}

// MergeTopologySpreadConstraints unions base and overlay by (topologyKey, whenUnsatisfiable);
// overlay wins when both define the same constraint key.
func MergeTopologySpreadConstraints(
	base, overlay []corev1.TopologySpreadConstraint,
) []corev1.TopologySpreadConstraint {
	if len(overlay) == 0 {
		return base
	}
	type key struct{ topologyKey, whenUnsatisfiable string }
	overlayKeys := make(map[key]struct{}, len(overlay))
	for _, c := range overlay {
		overlayKeys[key{c.TopologyKey, string(c.WhenUnsatisfiable)}] = struct{}{}
	}
	result := make([]corev1.TopologySpreadConstraint, 0, len(base)+len(overlay))
	for _, c := range base {
		if _, found := overlayKeys[key{c.TopologyKey, string(c.WhenUnsatisfiable)}]; !found {
			result = append(result, c)
		}
	}
	return append(result, overlay...)
}
