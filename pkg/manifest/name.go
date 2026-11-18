package manifest

import (
	"fmt"
	"regexp"
)

// workloadNamePattern is an RFC 1123 DNS label: lowercase alphanumerics and
// '-', starting and ending with an alphanumeric, at most 63 characters.
var workloadNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateWorkloadName rejects a Notebook or ModelService name that cannot be
// used verbatim as a runtime object name (container, StatefulSet, Deployment,
// Service) or in the browser proxy path. Names are validated, never
// sanitized: sanitizing maps distinct names onto one runtime object (two
// docker containers "My-Notebook") and leaves the proxy base URL unmatched.
func ValidateWorkloadName(name string) error {
	if !workloadNamePattern.MatchString(name) {
		return fmt.Errorf("metadata.name %q must be a DNS label: lowercase letters, digits and '-', starting and ending with a letter or digit, at most 63 characters", name)
	}
	return nil
}
