package notebook

import "context"

// Repository is the persistence interface for NotebookServer records.
type Repository interface {
	Create(ctx context.Context, nb *NotebookServer) error
	Get(ctx context.Context, name string) (*NotebookServer, error)
	GetByVolumeID(ctx context.Context, volumeID string) (*NotebookServer, error)
	Update(ctx context.Context, nb *NotebookServer) error
	SetStatus(ctx context.Context, name, status string) error
	List(ctx context.Context) ([]*NotebookServer, error)
	Delete(ctx context.Context, name string) error
}
