package taskruntime

import (
	"encoding/json"
	"fmt"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

// ResolveImage returns the container image for a task using the standard priority order:
//
//	step.runner.image → step.run.image → pipeline defaults.image → workerDefault
//
// Returns an error only when no image is found and workerDefault is empty.
// Must be called by the worker layer before passing ExecSpec to a Driver.
func ResolveImage(task *proto.Task, workerDefault string) (string, error) {
	var step pipeline.Step
	if len(task.Step) > 0 {
		_ = json.Unmarshal(task.Step, &step)
	}
	for _, img := range []string{step.Runner.Image, step.Run.Image} {
		if img != "" {
			return img, nil
		}
	}
	var pl pipeline.Pipeline
	if len(task.Pipeline) > 0 {
		_ = json.Unmarshal(task.Pipeline, &pl)
	}
	if pl.Spec.Defaults.Image != "" {
		return pl.Spec.Defaults.Image, nil
	}
	if workerDefault != "" {
		return workerDefault, nil
	}
	return "", fmt.Errorf("step %q: no container image configured (set step.run.image, spec.defaults.image, or --default-image)", task.StepName)
}

// ResolveNamespace extracts the K8s namespace from the task's pipeline placement.
// Falls back to defaultNamespace when none is specified in the pipeline.
// Must be called by the K8s worker layer; namespace policy validation (allowed
// namespaces list) is also the caller's responsibility.
func ResolveNamespace(task *proto.Task, defaultNamespace string) string {
	var pl pipeline.Pipeline
	if len(task.Pipeline) > 0 {
		_ = json.Unmarshal(task.Pipeline, &pl)
	}
	if pl.Spec.Placement.Namespace != "" {
		return pl.Spec.Placement.Namespace
	}
	return defaultNamespace
}
