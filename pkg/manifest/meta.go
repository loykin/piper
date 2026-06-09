package manifest

const APIVersion = "piper/v1"

const (
	KindPipeline         = "Pipeline"
	KindPipelineTemplate = "PipelineTemplate"
	KindNotebook         = "Notebook"
	KindModelService     = "ModelService"
)

type TypeMeta struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind"       json:"kind"`
}

type ObjectMeta struct {
	Name   string            `yaml:"name"             json:"name"`
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}
