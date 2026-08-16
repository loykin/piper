package credential

import "context"

type Repository interface {
	// List returns credential metadata for projectID, ordered by name.
	// limit 0 means no limit (return everything); offset is only meaningful
	// when limit > 0.
	List(ctx context.Context, projectID string, limit, offset int) ([]*Metadata, error)
	// Count returns the total number of credentials for projectID, ignoring
	// limit/offset.
	Count(ctx context.Context, projectID string) (int, error)
	Get(ctx context.Context, projectID, name string) (*Metadata, error)
	Create(ctx context.Context, meta *Metadata, encrypted []byte) error
	Rotate(ctx context.Context, projectID, name string, encrypted []byte, keys []string) error
	Patch(ctx context.Context, projectID, name string, req PatchRequest) error
	Delete(ctx context.Context, projectID, name string) error
	GetValue(ctx context.Context, projectID, name string) ([]byte, error)
	MarkUsed(ctx context.Context, projectID, name string) error
	RecordTestResult(ctx context.Context, projectID, name string, ok bool, message string) error
}
