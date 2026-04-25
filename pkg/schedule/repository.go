package schedule

import (
	"context"
	"time"
)

// Repository is the persistence interface for Schedule records.
type Repository interface {
	Create(ctx context.Context, sc *Schedule) error
	Get(ctx context.Context, id string) (*Schedule, error)
	List(ctx context.Context) ([]*Schedule, error)
	ListDue(ctx context.Context, now time.Time) ([]*Schedule, error)
	UpdateRun(ctx context.Context, id string, lastRunAt, nextRunAt time.Time) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Delete(ctx context.Context, id string) error
}
