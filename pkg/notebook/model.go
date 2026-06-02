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
	Name      string    `json:"name"       db:"name"`
	Status    string    `json:"status"     db:"status"`
	Env       string    `json:"env"        db:"env"`
	Endpoint  string    `json:"endpoint"   db:"endpoint"`
	PID       int       `json:"pid"        db:"pid"`
	WorkDir   string    `json:"work_dir"   db:"work_dir"`
	Token     string    `json:"token"      db:"token"`
	WorkerID  string    `json:"worker_id"  db:"worker_id"`
	VolumeID  string    `json:"volume_id"  db:"volume_id"`
	Image     string    `json:"image"      db:"image"`
	YAML      string    `json:"yaml"       db:"yaml"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
