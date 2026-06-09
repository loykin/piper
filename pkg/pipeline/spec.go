package pipeline

import (
	"fmt"

	"github.com/piper/piper/pkg/manifest"
)

// Pipeline is the top-level structure of a piper Pipeline YAML definition.
type Pipeline struct {
	manifest.TypeMeta `yaml:",inline"`
	Metadata          manifest.ObjectMeta `yaml:"metadata"`
	Spec              PipelineSpec        `yaml:"spec"`
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

type PipelineSpec struct {
	Defaults  *PipelineDefaults `yaml:"defaults,omitempty"`
	Steps     []Step            `yaml:"steps"`
	OnSuccess *OnSuccess        `yaml:"on_success,omitempty"`
}

// PipelineDefaults provides step-level defaults applied when a step omits driver fields.
// Routing (placement) and image defaults are expressed here so individual steps can override.
type PipelineDefaults struct {
	Driver manifest.DriverSpec `yaml:"driver,omitempty"`
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

type Step struct {
	Name      string               `yaml:"name"`
	Options   manifest.SpecOptions `yaml:"options,omitempty"`
	Run       Run                  `yaml:"run"`
	Driver    manifest.DriverSpec  `yaml:"driver,omitempty"`
	Params    map[string]any       `yaml:"params,omitempty"`
	Inputs    []Artifact           `yaml:"inputs,omitempty"`
	Outputs   []Artifact           `yaml:"outputs,omitempty"`
	DependsOn []string             `yaml:"depends_on,omitempty"`
}

type Run struct {
	Type           string     `yaml:"type"`   // notebook | python | command
	Source         string     `yaml:"source"` // git | s3 | http | local
	Repo           string     `yaml:"repo"`
	Branch         string     `yaml:"branch"`
	Path           string     `yaml:"path"`
	Notebook       string     `yaml:"notebook,omitempty"` // shorthand: type=notebook, source=local, path=<value>
	Deps           []string   `yaml:"deps,omitempty"`     // extra files/dirs to snapshot alongside the entry point
	Prepare        [][]string `yaml:"prepare,omitempty"`  // commands run before the entry point
	Dir            string     `yaml:"dir"`                // sub-directory name for the source checkout
	URL            string     `yaml:"url"`                // http/https URL (source: http)
	Command        []string   `yaml:"command"`
	SnapshotPrefix string     `yaml:"snapshot_prefix,omitempty"` // set at runtime after submit
}

type Artifact struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	From string `yaml:"from"` // "stepName/outputName"
}

// ApplyDefaults returns a copy of the pipeline with defaults.driver merged into
// every step that has not set its own value for each field. The original is not modified.
func (p *Pipeline) ApplyDefaults() *Pipeline {
	if p.Spec.Defaults == nil {
		return p
	}
	d := p.Spec.Defaults.Driver
	out := *p
	steps := make([]Step, len(p.Spec.Steps))
	for i, s := range p.Spec.Steps {
		s.Driver = mergeDriverSpec(d, s.Driver)
		steps[i] = s
	}
	spec := out.Spec
	spec.Steps = steps
	out.Spec = spec
	return &out
}

// mergeDriverSpec returns a DriverSpec where base fields fill in zero values of override.
func mergeDriverSpec(base, override manifest.DriverSpec) manifest.DriverSpec {
	if override.Image == "" {
		override.Image = base.Image
	}
	if override.Resources.CPU == "" {
		override.Resources.CPU = base.Resources.CPU
	}
	if override.Resources.Memory == "" {
		override.Resources.Memory = base.Resources.Memory
	}
	if override.Resources.GPU == "" {
		override.Resources.GPU = base.Resources.GPU
	}
	if override.Placement.Label == "" {
		override.Placement.Label = base.Placement.Label
	}
	if override.Placement.Worker == "" {
		override.Placement.Worker = base.Placement.Worker
	}
	if override.Placement.Runtime == "" {
		override.Placement.Runtime = base.Placement.Runtime
	}
	if override.K8s == nil && base.K8s != nil {
		override.K8s = base.K8s
	}
	if override.Docker == nil && base.Docker != nil {
		override.Docker = base.Docker
	}
	if override.Process == nil && base.Process != nil {
		override.Process = base.Process
	}
	return override
}
