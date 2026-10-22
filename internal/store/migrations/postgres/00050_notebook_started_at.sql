-- +goose Up
-- started_at: when the notebook's current run began (create or restart), so
-- the history row written when that run ends carries its own start time
-- instead of the notebook's first creation. Existing rows get created_at.
ALTER TABLE notebook_servers ADD COLUMN started_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
UPDATE notebook_servers SET started_at = created_at;

-- History is now written when a run ends. A notebook already stopped or
-- failed ended its last run before that, and under the new rule no later
-- action archives it, so record it here once.
INSERT INTO notebook_history (project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, reason, yaml, created_by, deployed_at, stopped_at)
SELECT project_id, name, status, env, '', 0, work_dir, runtime_id, volume_id, reason, yaml, created_by, started_at, updated_at
FROM notebook_servers WHERE status IN ('stopped', 'failed');

-- +goose Down
ALTER TABLE notebook_servers DROP COLUMN started_at;
