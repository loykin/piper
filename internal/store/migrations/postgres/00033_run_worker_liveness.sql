-- +goose Up
-- worker_last_seen_at backs a run-level heartbeat: the bound worker pushes
-- its set of currently-owned non-terminal run IDs on the existing
-- pipeline.lease_renew cadence, and the master touches this column for each.
-- Used by the master's staleness sweep to detect a permanently-lost worker
-- (stale timestamp AND absent from the live connection registry) instead of
-- leaving an orphaned run stuck "running" forever.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS worker_last_seen_at TIMESTAMPTZ;
-- cancel_requested_at records a cancel request the master couldn't relay to
-- the bound worker immediately (worker disconnected). Durable so the intent
-- survives until it can be delivered on reconnect/worker-restart, or acted
-- on directly by the staleness sweep if the worker never comes back.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_runs_worker_liveness ON runs(worker_id, worker_last_seen_at) WHERE status = 'running';

-- +goose Down
DROP INDEX IF EXISTS idx_runs_worker_liveness;
ALTER TABLE runs DROP COLUMN IF EXISTS worker_last_seen_at;
ALTER TABLE runs DROP COLUMN IF EXISTS cancel_requested_at;
