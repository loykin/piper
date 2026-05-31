package notebook

import "time"

const (
	StatusRunning = "running"
	StatusStopped = "stopped"
)

// NotebookServer represents a running (or stopped) Jupyter notebook server.
type NotebookServer struct {
	Name      string    `json:"name"               db:"name"`
	Status    string    `json:"status"             db:"status"`
	Endpoint  string    `json:"endpoint"           db:"endpoint"`
	PID       int       `json:"pid"                db:"pid"`
	WorkDir   string    `json:"work_dir"           db:"work_dir"`
	Token     string    `json:"token"              db:"token"`
	Image     string    `json:"image,omitempty"    db:"image"`
	Namespace string    `json:"namespace,omitempty" db:"namespace"`
	CreatedAt time.Time `json:"created_at"         db:"created_at"`
	UpdatedAt time.Time `json:"updated_at"         db:"updated_at"`
}
