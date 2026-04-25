package serving

import "time"

const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// Service represents a deployed ModelService record in the database.
type Service struct {
	Name      string    `json:"name"`
	RunID     string    `json:"run_id"`
	Artifact  string    `json:"artifact"`
	Status    string    `json:"status"`
	Endpoint  string    `json:"endpoint"`
	PID       int       `json:"pid"`
	YAML      string    `json:"yaml"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
