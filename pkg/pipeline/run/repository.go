package run

import (
	"context"
	"time"
)

// Repository is the persistence interface for Run records.
// Implemented by the SQLite store in internal/store/sqlite.
type Repository interface {
	Create(ctx context.Context, r *Run) error
	Get(ctx context.Context, projectID, id string) (*Run, error)
	List(ctx context.Context, projectID string, filter RunFilter) ([]*Run, error)
	// Count returns the number of runs matching filter, ignoring Limit/Offset.
	Count(ctx context.Context, projectID string, filter RunFilter) (int, error)
	UpdateStatus(ctx context.Context, projectID, id, status string, endedAt *time.Time) error
	MarkRunning(ctx context.Context, projectID, id string, startedAt time.Time) error
	Delete(ctx context.Context, projectID, id string) error
	GetLatestSuccessful(ctx context.Context, projectID, pipelineName string) (*Run, error)
	// ListTerminalBefore returns terminal (non-running, non-scheduled) runs
	// whose EndedAt is before cutoff, ordered oldest-first. Used by retention
	// cleanup so it only pulls candidates that might actually be expired,
	// instead of the project's entire run history.
	ListTerminalBefore(ctx context.Context, projectID string, cutoff time.Time) ([]*Run, error)
	// ExistingIDs returns the subset of ids that have a run row in any
	// project. Used by the orphan-artifact sweep to check a batch of
	// artifact-directory names against the DB in one query instead of one
	// lookup per directory.
	ExistingIDs(ctx context.Context, ids []string) (map[string]bool, error)
}

// StepRepository is the persistence interface for Step records.
type StepRepository interface {
	Upsert(ctx context.Context, s *Step) error
	List(ctx context.Context, projectID, runID string) ([]*Step, error)
	ListByRuns(ctx context.Context, projectID string, runIDs []string) (map[string][]*Step, error)
	DeleteByRun(ctx context.Context, projectID, runID string) error
}
