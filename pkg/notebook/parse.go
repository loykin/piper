package notebook

import (
	"fmt"

	"github.com/loykin/piper/pkg/manifest"
)

// Parse decodes and validates one Notebook manifest using the shared strict
// manifest contract.
func Parse(data []byte) (*Notebook, error) {
	var spec Notebook
	if err := manifest.DecodeStrict(data, &spec); err != nil {
		return nil, fmt.Errorf("parse Notebook YAML: %w", err)
	}
	if err := manifest.RequireTypeMeta(spec.TypeMeta, "Notebook"); err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := spec.Spec.Prepare.Validate(); err != nil {
		return nil, fmt.Errorf("invalid prepare spec: %w", err)
	}
	return &spec, nil
}
