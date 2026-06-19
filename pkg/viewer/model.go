package viewer

import "time"

type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusFailed   Status = "failed"
	StatusStopped  Status = "stopped"
)

// DefaultTTL is how long a viewer lives after creation.
const DefaultTTL = 2 * time.Hour

type Viewer struct {
	ID        string     `db:"id"         json:"id"`
	ProjectID string     `db:"project_id" json:"project_id"`
	Type      string     `db:"type"       json:"type"`
	RunID     string     `db:"run_id"     json:"run_id"`
	StepName  string     `db:"step_name"  json:"step_name"`
	Artifact  string     `db:"artifact"   json:"artifact"`
	Status    Status     `db:"status"     json:"status"`
	Endpoint  string     `db:"endpoint"   json:"endpoint,omitempty"`
	PID       int        `db:"pid"        json:"-"`
	WorkDir   string     `db:"work_dir"   json:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
	ExpiresAt *time.Time `db:"expires_at" json:"expires_at,omitempty"`
}

// ProxyURL returns the path prefix used to proxy this viewer.
func (v *Viewer) ProxyURL(projectID string) string {
	return "/projects/" + projectID + "/viewers/" + v.ID + "/proxy/"
}
