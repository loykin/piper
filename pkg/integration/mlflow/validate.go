package mlflow

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// validateIntegration is the write-time validation floor for this
// foundation phase: required fields, a valid ArtifactMode, and the
// TrackingURI SSRF checks (design doc section 5.3). Credential existence,
// kind, enabled state, and project scope are checked by the HTTP handler,
// where the credential store is available; repositories still enforce that
// the reference itself is non-empty for non-HTTP callers.
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

// ValidateDispatcherConfig validates the integrations.mlflow.* dispatcher
// settings both config layers (cmd/piper/config's CLI/viper config and
// piper.Config's runtime config) accept — a config field mistake must be
// rejected identically however the server is started, whether piper.New(cfg)
// is called directly or through the piper CLI's loader/validator.
func ValidateDispatcherConfig(dispatcherConcurrency, batchSize, maxAttemptsBeforeDead int, requestTimeout, leaseDuration, pollInterval time.Duration, allowedHosts, allowedCIDRs []string) error {
	if dispatcherConcurrency < 0 {
		return fmt.Errorf("integrations.mlflow.dispatcher_concurrency must not be negative")
	}
	if batchSize < 0 {
		return fmt.Errorf("integrations.mlflow.batch_size must not be negative")
	}
	if requestTimeout < 0 {
		return fmt.Errorf("integrations.mlflow.request_timeout must not be negative")
	}
	if maxAttemptsBeforeDead < 0 {
		return fmt.Errorf("integrations.mlflow.max_attempts_before_dead must not be negative")
	}
	if leaseDuration < 0 {
		return fmt.Errorf("integrations.mlflow.lease_duration must not be negative")
	}
	if pollInterval < 0 {
		return fmt.Errorf("integrations.mlflow.poll_interval must not be negative")
	}
	for _, host := range allowedHosts {
		if strings.TrimSpace(host) == "" {
			return fmt.Errorf("integrations.mlflow.allowed_hosts must not contain empty values")
		}
	}
	for _, cidr := range allowedCIDRs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return fmt.Errorf("integrations.mlflow.allowed_cidrs contains invalid CIDR %q", cidr)
		}
	}
	return nil
}
