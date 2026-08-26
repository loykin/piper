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
	Delete(ctx context.Context, projectID, name string) error
	// AppendHistory records nb as a past notebook lifecycle state in
	// notebook_history, stamped with the current time as its stopped/replaced
	// moment. Called both when a notebook is deleted and, before Restart
	// overwrites the current row, whenever a restart replaces an
	// already-running server — so no prior run of a notebook is silently
	// lost with no trace.
	AppendHistory(ctx context.Context, nb *NotebookServer) error
	// ListHistory returns notebook history for projectID, most recently
	// stopped first. limit 0 means no limit; offset is only meaningful when
	// limit > 0.
	ListHistory(ctx context.Context, projectID string, limit, offset int) ([]*NotebookHistory, error)
	// CountHistory returns the total number of notebook history rows for
	// projectID, ignoring limit/offset.
	CountHistory(ctx context.Context, projectID string) (int, error)
}
