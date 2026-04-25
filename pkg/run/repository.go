package run

import (
	"context"
	"time"
)

// Repository is the persistence interface for Run records.
// Implemented per-driver in internal/store/{sqlite,postgres,mysql}/.
type Repository interface {
	Create(ctx context.Context, r *Run) error
	Get(ctx context.Context, id string) (*Run, error)
	List(ctx context.Context, filter RunFilter) ([]*Run, error)
	UpdateStatus(ctx context.Context, id, status string, endedAt *time.Time) error
	MarkRunning(ctx context.Context, id string, startedAt time.Time) error
	Delete(ctx context.Context, id string) error
	GetLatestSuccessful(ctx context.Context, pipelineName string) (*Run, error)
}

// StepRepository is the persistence interface for Step records.
type StepRepository interface {
	Upsert(ctx context.Context, s *Step) error
	List(ctx context.Context, runID string) ([]*Step, error)
	DeleteByRun(ctx context.Context, runID string) error
}
