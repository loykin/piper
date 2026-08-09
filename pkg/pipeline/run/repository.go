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
	// FinalizeStatusCAS transitions a run to a terminal status (to must be one
	// of StatusSuccess/StatusFailed/StatusCanceled), but only if the row isn't
	// already terminal. A run can be created as either StatusScheduled or
	// StatusRunning depending on the entry path (see startRun vs createRun),
	// so this guards on "not already finalized" rather than a specific prior
	// status — a retried write whose earlier attempt already landed, or a
	// second finalize attempt racing the first, gets applied=false (not an
	// error) instead of clobbering whichever terminal status won first.
	FinalizeStatusCAS(ctx context.Context, projectID, id, to string, endedAt *time.Time) (applied bool, err error)
	MarkRunning(ctx context.Context, projectID, id string, startedAt time.Time) error
	// SetWorkerID records the worker this run is bound to (see AGENTS.md's
	// "Worker Assignment": one run always executes on one worker), but only
	// if the row doesn't already have one — a CAS on "worker_id = ''", not a
	// blind overwrite, so a second, differently-raced call can't reassign a
	// run to another worker after the fact. applied=false means the row
	// already had a different worker_id (or the row itself is missing) —
	// callers must check this rather than assuming success, since a caller
	// that treats this binding as an authorization root (see
	// pipelinedispatch.AgentBackend.Dispatch) would otherwise let a workload
	// go out to a worker the DB never actually confirmed as owner.
	SetWorkerID(ctx context.Context, projectID, id, workerID string) (applied bool, err error)
	// TouchWorkerLastSeen updates worker_last_seen_at for every run in
	// runIDs that is still bound to workerID — a run already rebound to a
	// different worker (or finalized) is silently skipped rather than
	// erroring, since the caller pushes its whole currently-owned run set on
	// a fixed cadence (see pipeline.lease_renew) and has no per-ID
	// success/failure to react to. This is the run-level equivalent of the
	// old step-level lease renewal, used by the master's staleness sweep to
	// tell "worker briefly slow to report" apart from "worker truly gone."
	TouchWorkerLastSeen(ctx context.Context, workerID string, runIDs []string) error
	// SetCancelRequested durably records that a cancel was requested for a
	// run whose bound worker couldn't be reached immediately (tunnel down),
	// so the intent survives until it can be delivered on reconnect/worker
	// restart, or acted on directly by the staleness sweep if the worker
	// never comes back. CAS on cancel_requested_at IS NULL, so a
	// duplicate/retried cancel call doesn't reset an already-pending
	// request's timestamp. applied=false means the run doesn't exist or a
	// cancel was already requested — both are fine to ignore, not errors.
	SetCancelRequested(ctx context.Context, projectID, id string) (applied bool, err error)
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
	// UpsertCAS behaves like Upsert but guards against two kinds of stale
	// write: a lower s.Attempts than the row's current attempts (a
	// delayed report for an earlier attempt that a newer attempt has since
	// superseded), and — at the *same* attempt — a write that would move a
	// terminal row (done/failed/skipped/canceled) to a non-terminal status
	// (a delayed "running" report arriving after "done" already landed for
	// that attempt, e.g. from worker restart/retransmission). Both cases
	// are silently dropped instead of clobbering the newer/terminal result;
	// applied=false (not an error) reports that back to the caller.
	UpsertCAS(ctx context.Context, s *Step) (applied bool, err error)
	List(ctx context.Context, projectID, runID string) ([]*Step, error)
	ListByRuns(ctx context.Context, projectID string, runIDs []string) (map[string][]*Step, error)
	DeleteByRun(ctx context.Context, projectID, runID string) error
	// ListNonTerminalByWorker returns every step assigned to workerID (across
	// all projects/runs) whose status is not yet terminal
	// (done/failed/skipped/canceled). A worker calls this on its own restart
	// to rebuild local scheduling state from the DB, rather than trusting any
	// state it held in memory before the restart — the DB row is the source
	// of truth for "what is this worker still responsible for," the same way
	// FinalizeStatusCAS's row check is the source of truth for "is this run
	// done."
	ListNonTerminalByWorker(ctx context.Context, workerID string) ([]*Step, error)
}
