package pipeline

import "fmt"

// Pipeline is the top-level structure of a piper YAML definition
type Pipeline struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

// Validate checks that the pipeline definition is structurally correct.
func (p *Pipeline) Validate() error {
	if p.Metadata.Name == "" {
		return fmt.Errorf("pipeline name is required")
	}
	if len(p.Spec.Steps) == 0 {
		return fmt.Errorf("pipeline must have at least one step")
	}
	names := make(map[string]bool)
	for _, s := range p.Spec.Steps {
		if s.Name == "" {
			return fmt.Errorf("step name is required")
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate step name: %s", s.Name)
		}
		names[s.Name] = true
	}
	for _, s := range p.Spec.Steps {
		for _, dep := range s.DependsOn {
			if !names[dep] {
				return fmt.Errorf("step %q depends on unknown step %q", s.Name, dep)
			}
		}
		for i, command := range s.Run.Prepare {
			if len(command) == 0 {
				return fmt.Errorf("step %q prepare[%d] command is empty", s.Name, i)
			}
		}
	}
	return nil
}

type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type Spec struct {
	Defaults  Defaults   `yaml:"defaults"`
	Placement Placement  `yaml:"placement,omitempty"`
	Steps     []Step     `yaml:"steps"`
	OnSuccess *OnSuccess `yaml:"on_success,omitempty"`
}

// Placement selects where the entire pipeline run should execute.
// Step-level placement is intentionally unsupported.
type Placement struct {
	Worker    string            `yaml:"worker,omitempty"`
	Cluster   string            `yaml:"cluster,omitempty"`
	Namespace string            `yaml:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

// OnSuccess defines actions to run when all pipeline steps succeed.
type OnSuccess struct {
	Deploy *DeployTrigger `yaml:"deploy,omitempty"`
}

// DeployTrigger automatically redeploys a ModelService after a successful run.
type DeployTrigger struct {
	Service  string `yaml:"service"`  // ModelService name
	Artifact string `yaml:"artifact"` // "step/artifact" from this run
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
	Env       map[string]string `yaml:"env,omitempty"`
	Resources Resources         `yaml:"resources"`
	Runner    RunnerSelector    `yaml:"runner"`
}

type Run struct {
	Type           string     `yaml:"type"`   // notebook | python | command
	Source         string     `yaml:"source"` // git | s3 | http | local
	Repo           string     `yaml:"repo"`
	Branch         string     `yaml:"branch"`
	Path           string     `yaml:"path"`
	Notebook       string     `yaml:"notebook,omitempty"` // shorthand: type=notebook, source=local, path=<value>
	Deps           []string   `yaml:"deps,omitempty"`     // extra files/dirs to snapshot alongside the entry point
	Prepare        [][]string `yaml:"prepare,omitempty"`  // commands run sequentially after source fetch and before the entry point
	Dir            string     `yaml:"dir"`                // sub-directory name for the source checkout (defaults to step name)
	URL            string     `yaml:"url"`                // http/https URL (source: http)
	Command        []string   `yaml:"command"`
	Image          string     `yaml:"image"`                     // Docker image to use for this step (optional)
	SnapshotPrefix string     `yaml:"snapshot_prefix,omitempty"` // set at runtime after submit; worker downloads this prefix
}

type Artifact struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	From string `yaml:"from"` // "stepName/outputName"
}

type Resources struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
	GPU    string `yaml:"gpu,omitempty"`
}

type RunnerSelector struct {
	Image          string            `yaml:"image"`
	Label          string            `yaml:"label"`
	NodeSelector   map[string]string `yaml:"node_selector,omitempty"`
	Tolerations    []Toleration      `yaml:"tolerations,omitempty"`
	PodLabels      map[string]string `yaml:"pod_labels,omitempty"`
	PodAnnotations map[string]string `yaml:"pod_annotations,omitempty"`
	SchedulerName  string            `yaml:"scheduler_name,omitempty"`
}

type Toleration struct {
	Key               string `yaml:"key,omitempty"`
	Operator          string `yaml:"operator,omitempty"`
	Value             string `yaml:"value,omitempty"`
	Effect            string `yaml:"effect,omitempty"`
	TolerationSeconds *int64 `yaml:"toleration_seconds,omitempty"`
}
