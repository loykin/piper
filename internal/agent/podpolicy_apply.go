package agent

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
)

func ApplyPodPolicyYAML[T any](
	yamlStr string,
	policy corev1.PodTemplateSpec,
	apply func(*T, corev1.PodTemplateSpec) bool,
) (string, error) {
	var manifest T
	if err := yaml.Unmarshal([]byte(yamlStr), &manifest); err != nil {
		return yamlStr, err
	}
	if !apply(&manifest, policy) {
		return yamlStr, nil
	}
	out, err := yaml.Marshal(&manifest)
	if err != nil {
		return yamlStr, err
	}
	return string(out), nil
}

func ApplyPodPolicyJSON[T any](
	jsonBytes []byte,
	policy corev1.PodTemplateSpec,
	apply func(*T, corev1.PodTemplateSpec) bool,
) ([]byte, error) {
	var manifest T
	if err := json.Unmarshal(jsonBytes, &manifest); err != nil {
		return jsonBytes, err
	}
	if !apply(&manifest, policy) {
		return jsonBytes, nil
	}
	out, err := json.Marshal(&manifest)
	if err != nil {
		return jsonBytes, err
	}
	return out, nil
}
