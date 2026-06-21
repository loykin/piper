package manifest

import (
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

// PlacementSpec controls which worker handles the workload and which runtime to use.
//
// Design invariant: one run executes on one worker. All steps in a run are
// dispatched to the same worker agent. Within K8s, node selection (GPU, CPU)
// is handled by driver.k8s.pod_template.spec.nodeSelector — not by placement.
// Multi-cluster routing is out of scope: run separate pipelines per cluster.
type PlacementSpec struct {
	// Label routes to any worker registered with this label (e.g. "gpu", "prod").
	// Workers register their label via --label flag.
	Label string `yaml:"label,omitempty"   json:"label,omitempty"`
	// Worker pins the run to a specific worker ID. Takes precedence over Label.
	Worker string `yaml:"worker,omitempty"  json:"worker,omitempty"`
	// Runtime selects the execution environment: baremetal | docker | k8s.
	Runtime string `yaml:"runtime,omitempty" json:"runtime,omitempty"`
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
	if s.PodTemplate.Spec.Containers != nil || s.PodTemplate.Spec.NodeSelector != nil ||
		s.PodTemplate.Spec.Tolerations != nil || s.PodTemplate.Spec.Volumes != nil ||
		s.PodTemplate.Spec.InitContainers != nil || s.PodTemplate.Spec.RuntimeClassName != nil ||
		s.PodTemplate.ObjectMeta.Labels != nil || s.PodTemplate.ObjectMeta.Annotations != nil {
		raw, err := sigsyaml.Marshal(s.PodTemplate)
		if err != nil {
			return nil, err
		}
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
