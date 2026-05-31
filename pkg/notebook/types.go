package notebook

// NotebookServerSpec is the top-level YAML definition for a notebook server.
//
// Example:
//
//	apiVersion: piper/v1
//	kind: NotebookServer
//	metadata:
//	  name: my-notebook
//	spec:
//	  runtime:
//	    mode: local   # or k8s
//	    port: 8888
//	    work_dir: ./notebooks
//	  k8s:
//	    namespace: default
//	    image: jupyter/scipy-notebook:latest
type NotebookServerSpec struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Runtime struct {
			Mode    string `yaml:"mode"`
			Port    int    `yaml:"port"`
			WorkDir string `yaml:"work_dir"`
			// GPUs selects GPU devices for local mode (e.g. "0", "0,1", "all", "none").
			GPUs string `yaml:"gpus"`
			// Worker pins startup to a specific worker agent by hostname.
			Worker string `yaml:"worker"`
		} `yaml:"runtime"`
		K8s struct {
			Namespace string `yaml:"namespace"`
			Image     string `yaml:"image"`
		} `yaml:"k8s"`
	} `yaml:"spec"`
}
