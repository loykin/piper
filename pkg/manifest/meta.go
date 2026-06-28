package manifest

type TypeMeta struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind"       json:"kind"`
}

type ObjectMeta struct {
	Name        string            `yaml:"name"                  json:"name"`
	Version     int               `yaml:"version,omitempty"     json:"version,omitempty"`
	ProjectID   string            `yaml:"project_id,omitempty"  json:"project_id,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"      json:"labels,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string          `yaml:"tags,omitempty"        json:"tags,omitempty"`
}
