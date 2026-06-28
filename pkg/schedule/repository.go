package schedule

import (
	"context"
	"time"
)

// Repository is the persistence interface for Schedule records.
type Repository interface {
	Create(ctx context.Context, sc *Schedule) error
	Get(ctx context.Context, projectID, id string) (*Schedule, error)
	List(ctx context.Context, projectID string) ([]*Schedule, error)
	ListWithMaxRuns(ctx context.Context) ([]*Schedule, error)
	// ListEnabled returns all enabled schedules across all projects.
	// Used on server startup to seed the in-memory Scheduler.
	ListEnabled(ctx context.Context) ([]*Schedule, error)
	ListDue(ctx context.Context, now time.Time) ([]*Schedule, error)

	// ClaimRun atomically advances next_run_at and records last_run_at = lastRunAt.
	// last_run_at is the tick claim time, not the run creation time — the actual run
	// is started by the caller after this returns true. If run creation subsequently
	// fails, last_run_at still reflects the attempted tick (tick-loss accepted policy).
	// Returns false if the schedule is disabled, deleted, modified, or already claimed.
	ClaimRun(ctx context.Context, projectID, id string, expectedAt, lastRunAt, nextRunAt time.Time) (bool, error)

	// AdvanceNextRun advances next_run_at without touching last_run_at (skip path).
	// Returns false if the schedule is disabled, deleted, modified, or already advanced.
	AdvanceNextRun(ctx context.Context, projectID, id string, expectedAt, nextRunAt time.Time) (bool, error)

	// ClaimOneShotRun atomically disables a once/immediate schedule and sets last_run_at.
	// last_run_at records the claim time, not run creation success (same policy as ClaimRun).
	// Returns false if stale callback fired after disable/delete/update.
	ClaimOneShotRun(ctx context.Context, projectID, id string, expectedAt, lastRunAt time.Time) (bool, error)

	SetEnabled(ctx context.Context, projectID, id string, enabled bool) error
	Delete(ctx context.Context, projectID, id string) error
}
