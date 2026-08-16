package notebook

import (
	"fmt"

	"github.com/loykin/piper/pkg/manifest"
)

// Parse decodes and validates one Notebook manifest using the shared strict
// manifest contract.
func Parse(data []byte) (*Notebook, error) {
	return parse(data, false)
}

// ParseForExistingVolume decodes and validates a Notebook manifest that will
// be bound to an already-provisioned volume (Manager.CreateWithVolume) —
// spec.volume.size isn't required on this path. See
// Notebook.ValidateForExistingVolume.
func ParseForExistingVolume(data []byte) (*Notebook, error) {
	return parse(data, true)
}

func parse(data []byte, existingVolume bool) (*Notebook, error) {
	var spec Notebook
	if err := manifest.DecodeStrict(data, &spec); err != nil {
		return nil, fmt.Errorf("parse Notebook YAML: %w", err)
	}
	if err := manifest.RequireTypeMeta(spec.TypeMeta, "Notebook"); err != nil {
		return nil, err
	}
	if existingVolume {
		if err := spec.ValidateForExistingVolume(); err != nil {
			return nil, err
		}
	} else if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := spec.Spec.Prepare.Validate(); err != nil {
		return nil, fmt.Errorf("invalid prepare spec: %w", err)
	}
	return &spec, nil
}
