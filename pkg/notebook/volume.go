package notebook

import (
	"context"
	"time"
)

const (
	VolumeStatusBound    = "bound"    // attached to a live server record
	VolumeStatusReleased = "released" // server deleted, data preserved, recoverable
)

// NotebookVolume represents persistent storage for a Jupyter notebook server.
// It exists independently of NotebookServer, analogous to a Kubernetes PersistentVolume.
type NotebookVolume struct {
	ID        string    `json:"id"         db:"id"`
	Label     string    `json:"label"      db:"label"`
	WorkDir   string    `json:"work_dir"   db:"work_dir"`
	Status    string    `json:"status"     db:"status"`
	WorkerID  string    `json:"worker_id"  db:"worker_id"` // node affinity; empty for network storage (e.g. K8s CSI)
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// VolumeRepository is the persistence interface for NotebookVolume records.
type VolumeRepository interface {
	Create(ctx context.Context, v *NotebookVolume) error
	Get(ctx context.Context, id string) (*NotebookVolume, error)
	List(ctx context.Context) ([]*NotebookVolume, error)
	Update(ctx context.Context, v *NotebookVolume) error
	SetStatus(ctx context.Context, id, status string) error
	Delete(ctx context.Context, id string) error
}
