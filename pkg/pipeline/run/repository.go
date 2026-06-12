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
	UpdateStatus(ctx context.Context, projectID, id, status string, endedAt *time.Time) error
	MarkRunning(ctx context.Context, projectID, id string, startedAt time.Time) error
	Delete(ctx context.Context, projectID, id string) error
	GetLatestSuccessful(ctx context.Context, projectID, pipelineName string) (*Run, error)
}

// StepRepository is the persistence interface for Step records.
type StepRepository interface {
	Upsert(ctx context.Context, s *Step) error
	List(ctx context.Context, projectID, runID string) ([]*Step, error)
	DeleteByRun(ctx context.Context, projectID, runID string) error
}
