package notebook

import "context"

// Repository is the persistence interface for NotebookServer records.
type Repository interface {
	Create(ctx context.Context, nb *NotebookServer) error
	Get(ctx context.Context, projectID, name string) (*NotebookServer, error)
	GetByVolumeID(ctx context.Context, projectID, volumeID string) (*NotebookServer, error)
	Update(ctx context.Context, nb *NotebookServer) error
	SetStatus(ctx context.Context, projectID, name, status string) error
	List(ctx context.Context, projectID string) ([]*NotebookServer, error)
	ListByWorker(ctx context.Context, workerID string) ([]*NotebookServer, error)
	Delete(ctx context.Context, projectID, name string) error
}
