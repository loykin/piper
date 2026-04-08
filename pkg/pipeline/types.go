package pipeline

// Pipeline은 piper YAML 전체 구조
type Pipeline struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type Spec struct {
	Defaults Defaults `yaml:"defaults"`
	Steps    []Step   `yaml:"steps"`
}

type Defaults struct {
	Image string `yaml:"image"`
}

type Step struct {
	Name      string            `yaml:"name"`
	Run       Run               `yaml:"run"`
	DependsOn []string          `yaml:"depends_on"`
	Inputs    []Artifact        `yaml:"inputs"`
	Outputs   []Artifact        `yaml:"outputs"`
	Params    map[string]any    `yaml:"params"`
	Resources Resources         `yaml:"resources"`
	Runner    RunnerSelector    `yaml:"runner"`
}

type Run struct {
	Type    string `yaml:"type"`    // notebook | python | command
	Source  string `yaml:"source"`  // git | s3 | local
	Repo    string `yaml:"repo"`
	Branch  string `yaml:"branch"`
	Path    string `yaml:"path"`
	Command []string `yaml:"command"`
	Image   string `yaml:"image"`   // 이 step에서 쓸 Docker 이미지 (optional)
}

type Artifact struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	From string `yaml:"from"` // "stepName/outputName"
}

type Resources struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

type RunnerSelector struct {
	Label string `yaml:"label"`
}
