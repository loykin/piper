package serving

import (
	"fmt"

	"github.com/loykin/piper/pkg/manifest"
)

// Parse decodes and validates one ModelService manifest using the shared
// strict manifest contract.
func Parse(data []byte) (*ModelService, error) {
	var spec ModelService
	if err := manifest.DecodeStrict(data, &spec); err != nil {
		return nil, fmt.Errorf("parse ModelService YAML: %w", err)
	}
	if err := manifest.RequireTypeMeta(spec.TypeMeta, "ModelService"); err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}
