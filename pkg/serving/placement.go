package serving

import "fmt"

// ValidateDirectPlacement rejects placement fields that are meaningless for
// a direct, in-process runtime driver: placement.worker/placement.label
// name a remote worker or capability label from the legacy remote-agent
// model, and placement.runtime must be empty or match the runtime this
// installation owns. allowedRuntime is the one placement.runtime value
// other than "" that is accepted (e.g. "docker", "baremetal", "k8s").
// Mirrors internal/pipelinedispatch's validateDirectPlacement (fed.md §13.6).
func ValidateDirectPlacement(spec ModelService, allowedRuntime string) error {
	p := spec.Spec.Driver.Placement
	if p.Worker != "" {
		return fmt.Errorf("placement.worker is not supported by an in-process runtime")
	}
	if p.Label != "" {
		return fmt.Errorf("placement.label is not supported by an in-process runtime")
	}
	if p.Runtime != "" && p.Runtime != allowedRuntime {
		return fmt.Errorf("placement.runtime must be %q or empty", allowedRuntime)
	}
	return nil
}
