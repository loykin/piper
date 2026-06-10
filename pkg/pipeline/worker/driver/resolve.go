package driver

import (
	"encoding/json"
	"fmt"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
)

// ResolveImage returns the container image for a task and runtime using the
// standard priority order:
//
//	step.driver.<runtime>.image → pipeline defaults.driver.<runtime>.image → workerDefault
//
// Returns an error only when no image is found and workerDefault is empty.
// Must be called by the worker layer before passing ExecSpec to a Driver.
func ResolveImage(task *proto.Task, runtime string, workerDefault string) (string, error) {
	var step pipeline.Step
	if len(task.Step) > 0 {
		_ = json.Unmarshal(task.Step, &step)
	}
	if image := runtimeImage(step.Driver, runtime); image != "" {
		return image, nil
	}
	var pl pipeline.Pipeline
	if len(task.Pipeline) > 0 {
		_ = json.Unmarshal(task.Pipeline, &pl)
	}
	if pl.Spec.Defaults != nil {
		if image := runtimeImage(pl.Spec.Defaults.Driver, runtime); image != "" {
			return image, nil
		}
	}
	if workerDefault != "" {
		return workerDefault, nil
	}
	return "", fmt.Errorf("step %q: no %s image configured (set step.driver.%s.image, spec.defaults.driver.%s.image, or --default-image)", task.StepName, runtime, runtime, runtime)
}

func runtimeImage(spec manifest.DriverSpec, runtime string) string {
	switch runtime {
	case "docker":
		if spec.Docker != nil {
			return spec.Docker.Image
		}
	case "k8s":
		if spec.K8s != nil {
			return spec.K8s.Image
		}
	}
	return ""
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
