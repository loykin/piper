package manifest

type TypeMeta struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind"       json:"kind"`
}

type ObjectMeta struct {
	Name      string            `yaml:"name"                json:"name"`
	ProjectID string            `yaml:"project_id,omitempty" json:"project_id,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"    json:"labels,omitempty"`
}
