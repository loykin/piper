package notebook

import (
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// NotebookServerSpec is the YAML definition for a notebook server.
//
// Process example:
//
//	metadata:
//	  name: my-notebook
//	spec:
//	  placement:
//	    worker: gpu-node1
//	  process:
//	    env: conda:ml-env
//	    gpus: "0,1"
//
// Docker example:
//
//	metadata:
//	  name: my-notebook
//	spec:
//	  docker:
//	    image: jupyter/scipy-notebook:latest
//	    cpus: "4"
//	    mem_limit: "8g"
//	    volumes: [datasets]
//	    deploy:
//	      resources:
//	        reservations:
//	          devices:
//	            - driver: nvidia
//	              count: "1"
//	              capabilities: [gpu]
//
// K8s example:
//
//	metadata:
//	  name: my-notebook
//	spec:
//	  k8s:
//	    image: jupyter/scipy-notebook:latest
//	    storage_size: 20Gi
//	    pod_template:
//	      spec:
//	        nodeSelector:
//	          accelerator: nvidia
//	        containers:
//	          - name: notebook
//	            resources:
//	              limits:
//	                nvidia.com/gpu: "1"
type NotebookServerSpec struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Placement *NotebookPlacement   `yaml:"placement,omitempty"`
		K8s       *NotebookK8sSpec     `yaml:"k8s,omitempty"`
		Docker    *NotebookDockerSpec  `yaml:"docker,omitempty"`
		Process   *NotebookProcessSpec `yaml:"process,omitempty"`
	} `yaml:"spec"`
}

// NotebookPlacement selects which worker handles this notebook.
// Applies to all runtimes.
type NotebookPlacement struct {
	Worker string `yaml:"worker,omitempty"`
}

// NotebookK8sSpec holds k8s-specific notebook settings.
// pod_template accepts corev1.PodTemplateSpec directly — use the same format
// as any Kubernetes manifest. The notebook container must be named "notebook".
type NotebookK8sSpec struct {
	Image        string                 `yaml:"image,omitempty"`
	StorageClass string                 `yaml:"storage_class,omitempty"`
	StorageSize  string                 `yaml:"storage_size,omitempty"`
	PodTemplate  corev1.PodTemplateSpec `yaml:"-"`
}

// podTemplateAlias is used only for YAML parsing to avoid embedding corev1.PodTemplateSpec
// directly in yaml.v3 decode (k8s types only have json tags; resource.Quantity is incompatible).
type podTemplateAlias struct {
	Image        string    `yaml:"image,omitempty"`
	StorageClass string    `yaml:"storage_class,omitempty"`
	StorageSize  string    `yaml:"storage_size,omitempty"`
	PodTemplate  yaml.Node `yaml:"pod_template,omitempty"`
}

// UnmarshalYAML handles pod_template by round-tripping through sigs.k8s.io/yaml
// so that native k8s field names (camelCase json tags) and resource.Quantity work.
func (s *NotebookK8sSpec) UnmarshalYAML(value *yaml.Node) error {
	var a podTemplateAlias
	if err := value.Decode(&a); err != nil {
		return err
	}
	s.Image = a.Image
	s.StorageClass = a.StorageClass
	s.StorageSize = a.StorageSize
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

// NotebookDockerSpec holds Docker-specific notebook settings in Docker Compose
// service format.
type NotebookDockerSpec struct {
	Image       string            `yaml:"image,omitempty"`
	CPUs        string            `yaml:"cpus,omitempty"`
	MemLimit    string            `yaml:"mem_limit,omitempty"`
	ShmSize     string            `yaml:"shm_size,omitempty"`
	ReadOnly    bool              `yaml:"read_only,omitempty"`
	User        string            `yaml:"user,omitempty"`
	Tmpfs       []string          `yaml:"tmpfs,omitempty"`
	NetworkMode string            `yaml:"network_mode,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Deploy      *DockerDeploySpec `yaml:"deploy,omitempty"`
}

// DockerDeploySpec mirrors the Docker Compose deploy.resources structure.
type DockerDeploySpec struct {
	Resources DockerDeployResources `yaml:"resources"`
}

type DockerDeployResources struct {
	Reservations DockerReservations `yaml:"reservations"`
}

type DockerReservations struct {
	Devices []DockerDeviceSpec `yaml:"devices,omitempty"`
}

// DockerDeviceSpec mirrors Docker Compose deploy.resources.reservations.devices.
type DockerDeviceSpec struct {
	Driver       string   `yaml:"driver,omitempty"`
	Count        string   `yaml:"count,omitempty"`      // "all" or a number string
	DeviceIDs    []string `yaml:"device_ids,omitempty"` // explicit GPU indices
	Capabilities []string `yaml:"capabilities,omitempty"`
}

// NotebookProcessSpec holds process-runtime-specific settings (custom format).
type NotebookProcessSpec struct {
	// Env is the Python environment.
	// venv path:  /path/to/venv
	// conda env:  conda:env-name
	// empty:      auto-create .venv in work dir
	Env string `yaml:"env,omitempty"`

	// GPUs selects GPU devices for CUDA_VISIBLE_DEVICES, e.g. "0", "0,1", "all".
	GPUs string `yaml:"gpus,omitempty"`
}

// WorkerID returns the placement worker, or empty string if unset.
func (s NotebookServerSpec) WorkerID() string {
	if s.Spec.Placement != nil {
		return s.Spec.Placement.Worker
	}
	return ""
}

// GPURequest returns a GPU device-ID selector string used to route the notebook
// to a worker that holds those specific GPUs.
//
// Only explicit device_ids produce a non-empty result, because a count ("1", "all")
// is a resource quantity — not a device address — and cannot identify a worker.
//
// For process: gpus field (e.g. "0,1" or "all").
// For docker: device_ids of the first gpu-capable device entry, or empty if only count is set.
// For k8s: always empty (node selection is handled by pod_template.spec.nodeSelector).
func (s NotebookServerSpec) GPURequest() string {
	if s.Spec.Process != nil && s.Spec.Process.GPUs != "" {
		return s.Spec.Process.GPUs
	}
	if s.Spec.Docker != nil && s.Spec.Docker.Deploy != nil {
		for _, dev := range s.Spec.Docker.Deploy.Resources.Reservations.Devices {
			if !slices.Contains(dev.Capabilities, "gpu") {
				continue
			}
			if len(dev.DeviceIDs) > 0 {
				return strings.Join(dev.DeviceIDs, ",")
			}
			// count-only: any GPU worker is acceptable; caller treats "" as "pick any"
			return ""
		}
	}
	return ""
}

// StorageSize returns the k8s PVC storage size, or empty if unset.
func (s NotebookServerSpec) StorageSize() string {
	if s.Spec.K8s != nil {
		return s.Spec.K8s.StorageSize
	}
	return ""
}
