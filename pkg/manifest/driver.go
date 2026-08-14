package manifest

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// DriverSpec describes where and how to run a workload.
// "What to run" (command, port, etc.) belongs in each domain's Run block.
type DriverSpec struct {
	Placement PlacementSpec      `yaml:"placement,omitempty" json:"placement,omitempty"`
	K8s       *DriverK8sSpec     `yaml:"k8s,omitempty"       json:"k8s,omitempty"`
	Docker    *DriverDockerSpec  `yaml:"docker,omitempty"    json:"docker,omitempty"`
	Process   *DriverProcessSpec `yaml:"process,omitempty"  json:"process,omitempty"`
}

// PlacementSpec selects the execution runtime. Each Piper installation owns
// exactly one runtime; a non-empty value must match that configured runtime.
type PlacementSpec struct {
	// Runtime selects the execution environment: baremetal | docker | k8s.
	Runtime string `yaml:"runtime,omitempty" json:"runtime,omitempty"`
}

// ValidateRuntimePlacement requires an explicit manifest runtime to match the
// single runtime owned by this Piper installation. Empty means use that owned
// runtime.
func ValidateRuntimePlacement(p PlacementSpec, ownedRuntime string) error {
	if ownedRuntime == "" {
		return nil
	}
	if p.Runtime != "" && p.Runtime != ownedRuntime {
		return fmt.Errorf("placement.runtime must be %q or empty", ownedRuntime)
	}
	return nil
}

// ResourceSpec is a Kubernetes resource hint.
// Translated to container resource requests and limits by K8s drivers.
type ResourceSpec struct {
	CPU    string `yaml:"cpu,omitempty"    json:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty"`
	GPU    string `yaml:"gpu,omitempty"    json:"gpu,omitempty"`
}

// DriverK8sSpec holds Kubernetes-specific driver settings.
// PodTemplate uses a custom UnmarshalYAML because yaml.v3 is incompatible with
// corev1 json tags and resource.Quantity — we round-trip through sigs.k8s.io/yaml.
type DriverK8sSpec struct {
	Image           string                 `yaml:"image,omitempty"             json:"image,omitempty"`
	Namespace       string                 `yaml:"namespace,omitempty"         json:"namespace,omitempty"`
	Replicas        int                    `yaml:"replicas,omitempty"          json:"replicas,omitempty"`
	ImagePullPolicy string                 `yaml:"image_pull_policy,omitempty" json:"image_pull_policy,omitempty"`
	Resources       ResourceSpec           `yaml:"resources,omitempty"         json:"resources,omitempty"`
	PodTemplate     corev1.PodTemplateSpec `yaml:"-"                           json:"pod_template,omitempty"`
}

type driverK8sAlias struct {
	Image           string       `yaml:"image,omitempty"`
	Namespace       string       `yaml:"namespace,omitempty"`
	Replicas        int          `yaml:"replicas,omitempty"`
	ImagePullPolicy string       `yaml:"image_pull_policy,omitempty"`
	Resources       ResourceSpec `yaml:"resources,omitempty"`
	PodTemplate     yaml.Node    `yaml:"pod_template,omitempty"`
}

func (s *DriverK8sSpec) UnmarshalYAML(value *yaml.Node) error {
	allowed := map[string]struct{}{
		"image": {}, "namespace": {}, "replicas": {}, "image_pull_policy": {},
		"resources": {}, "pod_template": {},
	}
	if value.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(value.Content); i += 2 {
			key := value.Content[i].Value
			if _, ok := allowed[key]; !ok {
				return fmt.Errorf("field %s not found in type manifest.DriverK8sSpec", key)
			}
		}
	}
	var a driverK8sAlias
	if err := value.Decode(&a); err != nil {
		return err
	}
	s.Image = a.Image
	s.Namespace = a.Namespace
	s.Replicas = a.Replicas
	s.ImagePullPolicy = a.ImagePullPolicy
	s.Resources = a.Resources
	if a.PodTemplate.Kind != 0 {
		raw, err := yaml.Marshal(&a.PodTemplate)
		if err != nil {
			return err
		}
		if err := sigsyaml.Unmarshal(raw, &s.PodTemplate); err != nil {
			return err
		}
	}
	return nil
}

func (s DriverK8sSpec) MarshalYAML() (interface{}, error) {
	type alias struct {
		Image           string       `yaml:"image,omitempty"`
		Namespace       string       `yaml:"namespace,omitempty"`
		Replicas        int          `yaml:"replicas,omitempty"`
		ImagePullPolicy string       `yaml:"image_pull_policy,omitempty"`
		Resources       ResourceSpec `yaml:"resources,omitempty"`
		PodTemplate     interface{}  `yaml:"pod_template,omitempty"`
	}
	a := alias{
		Image:           s.Image,
		Namespace:       s.Namespace,
		Replicas:        s.Replicas,
		ImagePullPolicy: s.ImagePullPolicy,
		Resources:       s.Resources,
	}
	// Serialize PodTemplate via sigs.k8s.io/yaml (handles resource.Quantity etc.)
	// then decode to a generic interface so yaml.v3 can encode it without corev1 tags.
	// Marshal first and check whether the output is non-empty — this avoids
	// a hard-coded field list that would silently drop policy-injected fields
	// (e.g. affinity, imagePullSecrets) missing from the original condition.
	raw, err := sigsyaml.Marshal(s.PodTemplate)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		var m interface{}
		if err := sigsyaml.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		a.PodTemplate = m
	}
	return a, nil
}

// DriverDockerSpec holds Docker-specific driver settings.
type DriverDockerSpec struct {
	Image       string            `yaml:"image,omitempty"        json:"image,omitempty"`
	CPUs        string            `yaml:"cpus,omitempty"         json:"cpus,omitempty"`
	MemLimit    string            `yaml:"mem_limit,omitempty"    json:"mem_limit,omitempty"`
	ShmSize     string            `yaml:"shm_size,omitempty"     json:"shm_size,omitempty"`
	ReadOnly    bool              `yaml:"read_only,omitempty"    json:"read_only,omitempty"`
	User        string            `yaml:"user,omitempty"         json:"user,omitempty"`
	NetworkMode string            `yaml:"network_mode,omitempty" json:"network_mode,omitempty"`
	Tmpfs       []string          `yaml:"tmpfs,omitempty"        json:"tmpfs,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"      json:"volumes,omitempty"`
	Deploy      *DockerDeploySpec `yaml:"deploy,omitempty"       json:"deploy,omitempty"`
}

// DockerDeploySpec mirrors Docker Compose deploy.resources for GPU reservations.
type DockerDeploySpec struct {
	Resources DockerDeployResources `yaml:"resources,omitempty" json:"resources,omitempty"`
}

type DockerDeployResources struct {
	Reservations *DockerReservations `yaml:"reservations,omitempty" json:"reservations,omitempty"`
}

type DockerReservations struct {
	Devices []DockerDevice `yaml:"devices,omitempty" json:"devices,omitempty"`
}

type DockerDevice struct {
	Driver       string   `yaml:"driver,omitempty"       json:"driver,omitempty"`
	Count        string   `yaml:"count,omitempty"        json:"count,omitempty"`
	DeviceIDs    []string `yaml:"device_ids,omitempty"   json:"device_ids,omitempty"`
	Capabilities []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

// DriverProcessSpec holds baremetal process-specific driver settings.
type DriverProcessSpec struct {
	// Env selects the Python environment: venv path, "conda:<name>", or empty for auto-detect.
	Env string `yaml:"env,omitempty"  json:"env,omitempty"`
	// GPUs sets CUDA_VISIBLE_DEVICES: "0", "0,1", "all".
	GPUs string `yaml:"gpus,omitempty" json:"gpus,omitempty"`
}
