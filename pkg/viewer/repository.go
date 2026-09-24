package viewer

import "context"

type Repository interface {
	Create(ctx context.Context, v *Viewer) error
	Get(ctx context.Context, id string) (*Viewer, error)
	List(ctx context.Context, projectID string) ([]*Viewer, error)
	// FindRunning returns an existing running viewer for the same artifact+type, or nil.
	FindRunning(ctx context.Context, projectID, runID, stepName, artifact, typ string) (*Viewer, error)
	UpdateStatus(ctx context.Context, id string, status Status, endpoint string, pid int, workDir string) error
	ListExpired(ctx context.Context) ([]*Viewer, error)
	// MarkStaleFailed marks all starting/running viewers as failed (called on server startup).
	MarkStaleFailed(ctx context.Context) error
	Delete(ctx context.Context, id string) error
}
