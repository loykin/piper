package serving

// ModelService is the top-level structure for a piper ModelService YAML definition.
type ModelService struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   Metadata         `yaml:"metadata"`
	Spec       ModelServiceSpec `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type ModelServiceSpec struct {
	Model   ModelRef    `yaml:"model"`
	Runtime RuntimeSpec `yaml:"runtime"`
	K8s     K8sSpec     `yaml:"k8s"`
}

type ModelRef struct {
	// FromArtifact references an artifact produced by a Pipeline run.
	FromArtifact *ArtifactRef `yaml:"from_artifact"`
}

// ArtifactRef identifies a specific artifact from a Pipeline step.
type ArtifactRef struct {
	Pipeline string `yaml:"pipeline"` // Pipeline metadata.name
	Step     string `yaml:"step"`     // step name
	Artifact string `yaml:"artifact"` // outputs[].name
	Run      string `yaml:"run"`      // "latest" | <run-id>
}

// RuntimeSpec describes the serving process to launch.
type RuntimeSpec struct {
	Image   string   `yaml:"image"`
	Command []string `yaml:"command"`
	Port    int      `yaml:"port"`
	Mode    string   `yaml:"mode"` // "local" | "k8s"
}

// K8sSpec holds Kubernetes-specific deployment options.
type K8sSpec struct {
	Namespace string            `yaml:"namespace"`
	Replicas  int               `yaml:"replicas"`
	Resources map[string]string `yaml:"resources"` // cpu, memory, gpu
}
