package notebook

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
// K8s example (future):
//
//	metadata:
//	  name: my-notebook
//	spec:
//	  image: jupyter/scipy-notebook:latest
type NotebookServerSpec struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		// Env is the Python environment for local mode.
		// venv path:   /path/to/venv
		// conda env:   conda:env-name
		Env string `yaml:"env"`

		// Image is the Docker image for k8s mode (future).
		Image string `yaml:"image"`

		// GPUs selects GPU devices, e.g. "0", "0,1", "all". Local mode only.
		GPUs string `yaml:"gpus"`

		// Worker pins launch to a specific worker node by hostname.
		Worker string `yaml:"worker"`
	} `yaml:"spec"`
}
