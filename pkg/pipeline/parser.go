package pipeline

import (
	"fmt"
	"os"

	"github.com/loykin/piper/pkg/manifest"
	"gopkg.in/yaml.v3"
)

func ParseFile(path string) (*Pipeline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read pipeline file: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Pipeline, error) {
	var p Pipeline
	if err := manifest.DecodeStrict(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline yaml: %w", err)
	}
	if err := manifest.RequireTypeMeta(p.TypeMeta, "Pipeline"); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Marshal serialises a Pipeline back to YAML bytes.
func Marshal(pl *Pipeline) ([]byte, error) {
	if pl == nil {
		return nil, fmt.Errorf("pipeline is required")
	}
	cp := *pl
	if cp.APIVersion == "" && cp.Kind == "" {
		cp.APIVersion = manifest.APIVersionV1
		cp.Kind = "Pipeline"
	}
	if err := manifest.RequireTypeMeta(cp.TypeMeta, "Pipeline"); err != nil {
		return nil, err
	}
	return yaml.Marshal(&cp)
}
