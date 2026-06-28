package schedule

import "context"

// FireFunc is called by the Scheduler when a schedule fires.
// Only the identifiers are passed; the caller reads state from DB.
type FireFunc func(ctx context.Context, projectID, scheduleID string)

// SchedulerAPI is the interface that Handler and other callers use to keep the
// in-memory Scheduler in sync with DB changes (create / enable / disable / delete).
type SchedulerAPI interface {
	Add(sc *Schedule) error
	Remove(scheduleID string)
}
