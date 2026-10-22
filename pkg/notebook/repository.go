package notebook

import (
	"context"
	"time"
)

// Repository is the persistence interface for NotebookServer records.
type Repository interface {
	Create(ctx context.Context, nb *NotebookServer) error
	Get(ctx context.Context, projectID, name string) (*NotebookServer, error)
	GetByVolumeID(ctx context.Context, projectID, volumeID string) (*NotebookServer, error)
	Update(ctx context.Context, nb *NotebookServer) error
	// SetStatus sets status and the reason for it ("" when there is none).
	SetStatus(ctx context.Context, projectID, name, status, reason string) error
	// List returns notebooks for projectID, newest first. limit 0 means no
	// limit; offset is only meaningful when limit > 0.
	List(ctx context.Context, projectID string, limit, offset int) ([]*NotebookServer, error)
	// Count returns the total number of notebooks for projectID, ignoring
	// limit/offset.
	Count(ctx context.Context, projectID string) (int, error)
	// Delete removes the notebook row. It records no history: the notebook's
	// last run was recorded when it ended (FinishRun).
	Delete(ctx context.Context, projectID, name string) error
	// StartRun marks the notebook starting and stamps started_at with the
	// current time: a new run begins (restart).
	StartRun(ctx context.Context, projectID, name string) error
	// FinishRun ends the current run: it sets status (stopped or failed) and
	// reason, clears the live connection fields, and records the run in
	// notebook_history — all in one transaction, and only if the notebook
	// was not already stopped or failed. A run has one history row: if the
	// runtime revived it on its own (K8s restarting a crashed container)
	// and it ends again, its row is updated to the latest end. finished reports whether this call
	// ended the run, so a run reported ended twice (Delete's stop racing the
	// driver's own report) is recorded once.
	FinishRun(ctx context.Context, projectID, name, status, reason string) (finished bool, err error)
	// ListHistory returns notebook history for projectID, most recently
	// stopped first. limit 0 means no limit; offset is only meaningful when
	// limit > 0.
	ListHistory(ctx context.Context, projectID string, limit, offset int) ([]*NotebookHistory, error)
	// CountHistory returns the total number of notebook history rows for
	// projectID, ignoring limit/offset.
	CountHistory(ctx context.Context, projectID string) (int, error)
	// PurgeHistoryBefore deletes notebook_history rows whose stopped_at is
	// strictly before cutoff, across all projects, and returns the number of
	// rows removed. Used by the retention.TTLPurge job.
	PurgeHistoryBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
