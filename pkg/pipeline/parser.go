package pipeline

import (
	"fmt"
	"os"

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
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline yaml: %w", err)
	}
	if err := validate(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func validate(p *Pipeline) error {
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

	// Validate depends_on references
	for _, s := range p.Spec.Steps {
		for _, dep := range s.DependsOn {
			if !names[dep] {
				return fmt.Errorf("step %q depends on unknown step %q", s.Name, dep)
			}
		}
	}
	return nil
}
