package pipeline

import (
	"fmt"

	"github.com/loykin/piper/pkg/manifest"
)

// ValidateRuntime checks every resolved step against the runtime owned by the
// Piper instance that is about to execute the run.
func ValidateRuntime(pl *Pipeline, ownedRuntime string) error {
	if pl == nil {
		return fmt.Errorf("pipeline is required")
	}
	resolved := pl.ApplyDefaults()
	for _, step := range resolved.Spec.Steps {
		if err := manifest.ValidateRuntimePlacement(step.Driver.Placement, ownedRuntime); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	}
	return nil
}
