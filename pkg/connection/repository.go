package connection

import "context"

type Repository interface {
	List(ctx context.Context, projectID string) ([]*Metadata, error)
	Get(ctx context.Context, projectID, name string) (*Metadata, error)
	Create(ctx context.Context, meta *Metadata, encrypted []byte) error
	Rotate(ctx context.Context, projectID, name string, encrypted []byte) error
	Patch(ctx context.Context, projectID, name string, req PatchRequest) error
	Delete(ctx context.Context, projectID, name string) error
	GetValue(ctx context.Context, projectID, name string) ([]byte, error)
	MarkUsed(ctx context.Context, projectID, name string) error
	RecordTestResult(ctx context.Context, projectID, name string, ok bool, message string) error
}
