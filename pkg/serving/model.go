package serving

import (
	"time"

	"github.com/piper/piper/pkg/secret"
)

const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// Service represents a deployed ModelService record in the database.
type Service struct {
	Name      string    `json:"name"       db:"name"`
	OwnerID   string    `json:"owner_id,omitempty" db:"owner_id"`
	RunID     string    `json:"run_id"     db:"run_id"`
	Artifact  string    `json:"artifact"   db:"artifact"`
	Status    string    `json:"status"     db:"status"`
	Endpoint  string    `json:"endpoint"   db:"endpoint"`
	Namespace string    `json:"namespace,omitempty" db:"namespace"`
	PID       int       `json:"pid"        db:"pid"`
	YAML      string    `json:"yaml"       db:"yaml"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Redact returns a copy of the Service with sensitive fields masked.
func (s *Service) Redact() *Service {
	if s == nil {
		return nil
	}
	cp := *s
	cp.YAML = secret.RedactString(cp.YAML)
	return &cp
}

// K8sNamespace returns the Kubernetes namespace for the service, defaulting to "default".
func (s *Service) K8sNamespace() string {
	if s != nil && s.Namespace != "" {
		return s.Namespace
	}
	return "default"
}
