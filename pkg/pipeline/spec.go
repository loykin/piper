package pipeline

import (
	"fmt"
	"path"
	"strings"

	"github.com/loykin/piper/pkg/manifest"
)

// Pipeline is the top-level structure of a piper Pipeline YAML definition.
type Pipeline struct {
	manifest.TypeMeta `yaml:",inline"`
	Metadata          manifest.ObjectMeta `yaml:"metadata"`
	Spec              PipelineSpec        `yaml:"spec"`
}

// Validate checks that the pipeline definition is structurally correct.
func (p *Pipeline) Validate() error {
	if err := manifest.ValidateTypeMeta(p.TypeMeta, "Pipeline"); err != nil {
		return err
	}
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
		if err := validatePathComponent("step name", s.Name); err != nil {
			return err
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
		for field, value := range map[string]string{
			"run.path": s.Run.Path, "run.notebook": s.Run.Notebook, "run.dir": s.Run.Dir,
		} {
			if err := validateRelativePath(fmt.Sprintf("step %q %s", s.Name, field), value, true); err != nil {
				return err
			}
		}
		for i, dep := range s.Run.Deps {
			if err := validateRelativePath(fmt.Sprintf("step %q run.deps[%d]", s.Name, i), dep, false); err != nil {
				return err
			}
		}
		for i, input := range s.Inputs {
			if err := validatePathComponent(fmt.Sprintf("step %q inputs[%d].name", s.Name, i), input.Name); err != nil {
				return err
			}
			parts := strings.Split(input.From, "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("step %q inputs[%d].from must be stepName/artifactName", s.Name, i)
			}
			if !names[parts[0]] {
				return fmt.Errorf("step %q inputs[%d] references unknown step %q", s.Name, i, parts[0])
			}
			if err := validatePathComponent(fmt.Sprintf("step %q inputs[%d] artifact", s.Name, i), parts[1]); err != nil {
				return err
			}
		}
		for i, output := range s.Outputs {
			if err := validatePathComponent(fmt.Sprintf("step %q outputs[%d].name", s.Name, i), output.Name); err != nil {
				return err
			}
			if err := validateRelativePath(fmt.Sprintf("step %q outputs[%d].path", s.Name, i), output.Path, false); err != nil {
				return err
			}
		}
	}
	resolved := p.ApplyDefaults()
	for _, s := range resolved.Spec.Steps {
		switch s.Driver.Placement.Runtime {
		case "", "baremetal":
		case "docker":
			if s.Driver.Docker == nil || s.Driver.Docker.Image == "" {
				return fmt.Errorf("step %q: driver.docker.image is required", s.Name)
			}
		case "k8s":
			if s.Driver.K8s == nil || s.Driver.K8s.Image == "" {
				return fmt.Errorf("step %q: driver.k8s.image is required", s.Name)
			}
			if s.Driver.K8s.Namespace == "" {
				return fmt.Errorf("step %q: driver.k8s.namespace is required", s.Name)
			}
		default:
			return fmt.Errorf("step %q: unsupported driver.placement.runtime %q", s.Name, s.Driver.Placement.Runtime)
		}
	}
	return nil
}

func validatePathComponent(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%s must be a single path-safe name", label)
	}
	return nil
}

func validateRelativePath(label, value string, allowEmpty bool) error {
	if value == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%s is required", label)
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("%s must be relative", label)
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return fmt.Errorf("%s must not escape its workspace", label)
		}
	}
	if path.Clean(normalized) == ".." || strings.HasPrefix(path.Clean(normalized), "../") {
		return fmt.Errorf("%s must not escape its workspace", label)
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
	Type   string `yaml:"type" json:"type,omitempty"`     // notebook | python | command
	Source string `yaml:"source" json:"source,omitempty"` // git | s3 | http | local
	Repo   string `yaml:"repo" json:"repo,omitempty"`
	Branch string `yaml:"branch" json:"branch,omitempty"`
	Path   string `yaml:"path" json:"path,omitempty"`
	// CredentialRef names a git credential to use for git auth.
	CredentialRef  string     `yaml:"credentialRef,omitempty" json:"credentialRef,omitempty"`
	Notebook       string     `yaml:"notebook,omitempty" json:"notebook,omitempty"` // shorthand: type=notebook, source=local, path=<value>
	Deps           []string   `yaml:"deps,omitempty" json:"deps,omitempty"`         // extra files/dirs to snapshot alongside the entry point
	Prepare        [][]string `yaml:"prepare,omitempty" json:"prepare,omitempty"`   // commands run before the entry point
	Dir            string     `yaml:"dir" json:"dir,omitempty"`                     // sub-directory name for the source checkout
	URL            string     `yaml:"url" json:"url,omitempty"`                     // http/https URL (source: http)
	Command        []string   `yaml:"command" json:"command,omitempty"`
	SnapshotPrefix string     `yaml:"snapshot_prefix,omitempty" json:"snapshot_prefix,omitempty"` // set at runtime after submit
}

type Artifact struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	From string `yaml:"from"`           // "stepName/outputName"
	Type string `yaml:"type,omitempty"` // viewer hint: tensorboard, html, table, image
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
	if override.Placement.Runtime == "" {
		override.Placement.Runtime = base.Placement.Runtime
	}
	override.K8s = mergeK8sDriverSpec(base.K8s, override.K8s)
	override.Docker = mergeDockerDriverSpec(base.Docker, override.Docker)
	if override.Process == nil && base.Process != nil {
		override.Process = base.Process
	}
	return override
}

func mergeK8sDriverSpec(base, override *manifest.DriverK8sSpec) *manifest.DriverK8sSpec {
	if base == nil {
		return override
	}
	if override == nil {
		out := *base
		return &out
	}
	out := *override
	if out.Image == "" {
		out.Image = base.Image
	}
	if out.Namespace == "" {
		out.Namespace = base.Namespace
	}
	if out.Replicas == 0 {
		out.Replicas = base.Replicas
	}
	if out.ImagePullPolicy == "" {
		out.ImagePullPolicy = base.ImagePullPolicy
	}
	if out.Resources.CPU == "" {
		out.Resources.CPU = base.Resources.CPU
	}
	if out.Resources.Memory == "" {
		out.Resources.Memory = base.Resources.Memory
	}
	if out.Resources.GPU == "" {
		out.Resources.GPU = base.Resources.GPU
	}
	if len(out.PodTemplate.Spec.Containers) == 0 {
		out.PodTemplate = *base.PodTemplate.DeepCopy()
	}
	return &out
}

func mergeDockerDriverSpec(base, override *manifest.DriverDockerSpec) *manifest.DriverDockerSpec {
	if base == nil {
		return override
	}
	if override == nil {
		out := *base
		return &out
	}
	out := *override
	if out.Image == "" {
		out.Image = base.Image
	}
	return &out
}
