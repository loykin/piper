package serving

import "time"

const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// Service represents a deployed ModelService record in the database.
type Service struct {
	Name      string    `json:"name"       db:"name"`
	RunID     string    `json:"run_id"     db:"run_id"`
	Artifact  string    `json:"artifact"   db:"artifact"`
	Status    string    `json:"status"     db:"status"`
	Endpoint  string    `json:"endpoint"   db:"endpoint"`
	PID       int       `json:"pid"        db:"pid"`
	YAML      string    `json:"yaml"       db:"yaml"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
