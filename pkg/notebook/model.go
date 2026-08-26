package notebook

import "time"

const (
	StatusProvisioning = "provisioning" // volume being allocated
	StatusStarting     = "starting"     // process/pod starting up; env install may be running
	StatusRunning      = "running"
	StatusStopping     = "stopping" // stop requested, waiting for process exit
	StatusStopped      = "stopped"
	StatusFailed       = "failed"
)

// NotebookServer represents a running (or stopped) Jupyter notebook server.
// Each server is backed by a NotebookVolume that persists independently.
type NotebookServer struct {
	ProjectID string    `json:"project_id" db:"project_id"`
	Name      string    `json:"name"       db:"name"`
	Status    string    `json:"status"     db:"status"`
	Env       string    `json:"env"        db:"env"`
	Endpoint  string    `json:"endpoint"   db:"endpoint"`
	PID       int       `json:"pid"        db:"pid"`
	WorkDir   string    `json:"work_dir"   db:"work_dir"`
	Token     string    `json:"token"      db:"token"`
	RuntimeID string    `json:"runtime_id" db:"runtime_id"`
	VolumeID  string    `json:"volume_id"  db:"volume_id"`
	Image     string    `json:"image"      db:"image"`
	YAML      string    `json:"yaml"       db:"yaml"`
	CreatedBy string    `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// NotebookHistory is an immutable record of a past notebook lifecycle
// (a prior run before a restart, or the final state before delete). Token is
// deliberately omitted — it is a live connection secret, not history.
type NotebookHistory struct {
	ID         int       `json:"id"          db:"id"`
	ProjectID  string    `json:"project_id"  db:"project_id"`
	Name       string    `json:"name"        db:"name"`
	Status     string    `json:"status"      db:"status"`
	Env        string    `json:"env"         db:"env"`
	Endpoint   string    `json:"endpoint"    db:"endpoint"`
	PID        int       `json:"pid"         db:"pid"`
	WorkDir    string    `json:"work_dir"    db:"work_dir"`
	RuntimeID  string    `json:"runtime_id"  db:"runtime_id"`
	VolumeID   string    `json:"volume_id"   db:"volume_id"`
	Image      string    `json:"image"       db:"image"`
	YAML       string    `json:"yaml"        db:"yaml"`
	CreatedBy  string    `json:"created_by,omitempty" db:"created_by"`
	DeployedAt time.Time `json:"deployed_at" db:"deployed_at"`
	StoppedAt  time.Time `json:"stopped_at"  db:"stopped_at"`
}
