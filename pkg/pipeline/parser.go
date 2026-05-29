package pipeline

import (
	"bytes"
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
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline yaml: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}
