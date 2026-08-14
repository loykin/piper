package serving

import "github.com/loykin/piper/pkg/manifest"

// ValidateDirectPlacement requires placement.runtime to be empty or match the
// runtime this installation owns. allowedRuntime is the one value
// other than "" that is accepted (e.g. "docker", "baremetal", "k8s").
// Mirrors internal/pipelinedispatch's validateDirectPlacement (fed.md §13.6).
func ValidateDirectPlacement(spec ModelService, allowedRuntime string) error {
	return manifest.ValidateRuntimePlacement(spec.Spec.Driver.Placement, allowedRuntime)
}
