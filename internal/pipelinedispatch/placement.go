package pipelinedispatch

import (
	"encoding/json"
	"fmt"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
)

// validateDirectPlacement rejects placement fields that are meaningless for a
// direct, in-process runtime backend: placement.worker/placement.label name a
// remote worker or capability label, and placement.runtime must be empty or
// match the runtime this backend owns. label prefixes error messages (e.g.
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
	check := func(scope, worker, plabel, runtime string) error {
		if worker != "" {
			return fmt.Errorf("%s %s: placement.worker is not supported by an in-process runtime", label, scope)
		}
		if plabel != "" {
			return fmt.Errorf("%s %s: placement.label is not supported by an in-process runtime", label, scope)
		}
		if runtime != "" && runtime != allowedRuntime {
			return fmt.Errorf("%s %s: placement.runtime must be %s or empty", label, scope, allowedRuntime)
		}
		return nil
	}
	if pl.Spec.Defaults != nil {
		p := pl.Spec.Defaults.Driver.Placement
		if err := check("defaults", p.Worker, p.Label, p.Runtime); err != nil {
			return err
		}
	}
	for i := range pl.Spec.Steps {
		p := pl.Spec.Steps[i].Driver.Placement
		if err := check(fmt.Sprintf("step %q", pl.Spec.Steps[i].Name), p.Worker, p.Label, p.Runtime); err != nil {
			return err
		}
	}
	return nil
}
