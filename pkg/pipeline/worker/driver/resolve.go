package driver

import (
	"encoding/json"
	"fmt"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline"
)

// ResolveImage returns the container image for a task using the standard priority order:
//
//	step.driver.image → pipeline defaults.driver.image → workerDefault
//
// Returns an error only when no image is found and workerDefault is empty.
// Must be called by the worker layer before passing ExecSpec to a Driver.
func ResolveImage(task *proto.Task, workerDefault string) (string, error) {
	var step pipeline.Step
	if len(task.Step) > 0 {
		_ = json.Unmarshal(task.Step, &step)
	}
	if step.Driver.Image != "" {
		return step.Driver.Image, nil
	}
	var pl pipeline.Pipeline
	if len(task.Pipeline) > 0 {
		_ = json.Unmarshal(task.Pipeline, &pl)
	}
	if pl.Spec.Defaults != nil && pl.Spec.Defaults.Driver.Image != "" {
		return pl.Spec.Defaults.Driver.Image, nil
	}
	if workerDefault != "" {
		return workerDefault, nil
	}
	return "", fmt.Errorf("step %q: no container image configured (set step.driver.image, spec.defaults.driver.image, or --default-image)", task.StepName)
}

// ResolveNamespace extracts the K8s namespace from the task's step driver or pipeline defaults.
// Falls back to defaultNamespace when none is specified.
// Must be called by the K8s worker layer; namespace policy validation is the caller's responsibility.
func ResolveNamespace(task *proto.Task, defaultNamespace string) string {
	var step pipeline.Step
	if len(task.Step) > 0 {
		_ = json.Unmarshal(task.Step, &step)
	}
	if step.Driver.K8s != nil && step.Driver.K8s.Namespace != "" {
		return step.Driver.K8s.Namespace
	}
	var pl pipeline.Pipeline
	if len(task.Pipeline) > 0 {
		_ = json.Unmarshal(task.Pipeline, &pl)
	}
	if pl.Spec.Defaults != nil && pl.Spec.Defaults.Driver.K8s != nil && pl.Spec.Defaults.Driver.K8s.Namespace != "" {
		return pl.Spec.Defaults.Driver.K8s.Namespace
	}
	return defaultNamespace
}
