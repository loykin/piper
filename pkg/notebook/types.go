package notebook

import "github.com/piper/piper/pkg/pipeline"

// NotebookServerSpec is the YAML definition for a notebook server.
//
// Local example:
//
//	metadata:
//	  name: my-notebook
//	spec:
//	  env: /Users/me/project/venv   # or "conda:ml-env"
//	  gpus: "0,1"
//
// K8s example:
//
//	metadata:
//	  name: my-notebook
//	spec:
//	  image: jupyter/scipy-notebook:latest
//	  resources:
//	    cpu: "2"
//	    memory: "4Gi"
type NotebookServerSpec struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		// Env is the Python environment for local mode.
		// venv path:   /path/to/venv
		// conda env:   conda:env-name
		Env string `yaml:"env"`

		// Image is the Docker image for k8s mode.
		Image string `yaml:"image"`

		// GPUs selects GPU devices, e.g. "0", "0,1", "all". Local mode only.
		GPUs string `yaml:"gpus"`

		// Worker pins launch to a specific worker node by hostname.
		Worker string `yaml:"worker"`

		// Volumes selects additional worker-configured volume names for Docker mode.
		Volumes []string `yaml:"volumes,omitempty"`

		// K8s pod overrides — take precedence over NotebookK8sConfig.PodDefaults.
		Resources    pipeline.Resources    `yaml:"resources,omitempty"`
		NodeSelector map[string]string     `yaml:"node_selector,omitempty"`
		Tolerations  []pipeline.Toleration `yaml:"tolerations,omitempty"`
		StorageSize  string                `yaml:"storage_size,omitempty"`
	} `yaml:"spec"`
}
