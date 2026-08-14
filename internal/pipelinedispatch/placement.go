package pipelinedispatch

import (
	"encoding/json"
	"fmt"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
)

// validateDirectPlacement ensures placement.runtime is empty or matches the
// runtime this direct, in-process backend owns. Removed placement fields are
// rejected earlier by strict manifest decoding. label prefixes error messages (e.g.
// "k8s runtime", "docker runtime") and allowedRuntime is the one placement.runtime
// value other than "" that is accepted.
func validateDirectPlacement(task *proto.Task, label, allowedRuntime string) error {
	if task == nil {
		return fmt.Errorf("%s task is required", label)
	}
	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return fmt.Errorf("%s unmarshal pipeline: %w", label, err)
	}
	check := func(scope, runtime string) error {
		if runtime != "" && runtime != allowedRuntime {
			return fmt.Errorf("%s %s: placement.runtime must be %s or empty", label, scope, allowedRuntime)
		}
		return nil
	}
	if pl.Spec.Defaults != nil {
		p := pl.Spec.Defaults.Driver.Placement
		if err := check("defaults", p.Runtime); err != nil {
			return err
		}
	}
	for i := range pl.Spec.Steps {
		p := pl.Spec.Steps[i].Driver.Placement
		if err := check(fmt.Sprintf("step %q", pl.Spec.Steps[i].Name), p.Runtime); err != nil {
			return err
		}
	}
	return nil
}
