package serving

import (
	"fmt"
	"github.com/piper/piper/pkg/manifest"
)

// ModelService is the top-level structure for a piper ModelService YAML definition.
type ModelService struct {
	manifest.TypeMeta `yaml:",inline"`
	Metadata          manifest.ObjectMeta `yaml:"metadata"`
	Spec              ModelServiceSpec    `yaml:"spec"`
}

func (s ModelService) Validate() error {
	switch s.Spec.Driver.Placement.Runtime {
	case "docker":
		if s.Spec.Driver.Docker == nil || s.Spec.Driver.Docker.Image == "" {
			return fmt.Errorf("driver.docker.image is required")
		}
	case "k8s":
		if s.Spec.Driver.K8s == nil || s.Spec.Driver.K8s.Image == "" {
			return fmt.Errorf("driver.k8s.image is required")
		}
		if s.Spec.Driver.K8s.Namespace == "" {
			return fmt.Errorf("driver.k8s.namespace is required")
		}
	}
	return nil
}

type ModelServiceSpec struct {
	Options manifest.SpecOptions `yaml:"options,omitempty"`
	Model   ModelRef             `yaml:"model"`
	Run     ModelServiceRun      `yaml:"run"`
	Driver  manifest.DriverSpec  `yaml:"driver"`
}

// ModelServiceRun describes the serving process itself ("what to run").
// Separated from Driver ("where/how to run") to keep concerns clean.
type ModelServiceRun struct {
	Command    []string `yaml:"command"`
	Port       int      `yaml:"port"`
	HealthPath string   `yaml:"health_path,omitempty"` // readiness check path (default: "/")
}

type ModelRef struct {
	// FromArtifact references an artifact produced by a Pipeline run.
	FromArtifact *ArtifactRef `yaml:"from_artifact"`
	// FromURI references an external model location, e.g. file://, s3://, http://.
	FromURI string `yaml:"from_uri"`
}

// ArtifactRef identifies a specific artifact from a Pipeline step.
type ArtifactRef struct {
	Pipeline string `yaml:"pipeline"` // Pipeline metadata.name
	Step     string `yaml:"step"`     // step name
	Artifact string `yaml:"artifact"` // outputs[].name
	Run      string `yaml:"run"`      // "latest" | <run-id>
}
