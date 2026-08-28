package mlflow

import (
	"fmt"
	"strings"
)

// validateIntegration is the write-time validation floor for this
// foundation phase: required fields, a valid ArtifactMode, and the
// TrackingURI SSRF checks (design doc section 5.3). It intentionally does
// not check CredentialRef against the credential store (kind, existence,
// project scope) — that requires a credential.Store dependency the future
// service layer injects; repositories only persist the reference string.
func validateIntegration(m *MLflowIntegration, policy SSRFPolicy) error {
	if m == nil {
		return fmt.Errorf("%w: integration is required", ErrInvalid)
	}
	if strings.TrimSpace(m.ProjectID) == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if strings.TrimSpace(m.CredentialRef) == "" {
		return fmt.Errorf("%w: credential_ref is required", ErrInvalid)
	}
	if err := ValidateTrackingURI(m.TrackingURI, policy); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	switch ArtifactMode(m.ArtifactMode) {
	case ArtifactModeReference:
		// only mode enabled in v1
	case ArtifactModeMirrorSelected, ArtifactModeMirrorAll:
		return fmt.Errorf("%w: artifact_mode %q is reserved for a future phase, v1 only supports %q", ErrInvalid, m.ArtifactMode, ArtifactModeReference)
	default:
		return fmt.Errorf("%w: artifact_mode must be %q", ErrInvalid, ArtifactModeReference)
	}
	return nil
}
