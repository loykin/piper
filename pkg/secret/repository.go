package secret

import "context"

type Repository interface {
	List(ctx context.Context, projectID string) ([]*Metadata, error)
	Get(ctx context.Context, projectID, name string) (*Metadata, error)
	Create(ctx context.Context, meta *Metadata, encrypted []byte) error
	Rotate(ctx context.Context, projectID, name string, encrypted []byte, keys []string) error
	SetEnabled(ctx context.Context, projectID, name string, enabled bool) error
	Delete(ctx context.Context, projectID, name string) error
	GetActiveValue(ctx context.Context, projectID, name string) ([]byte, error)
	MarkUsed(ctx context.Context, projectID, name string) error
}
