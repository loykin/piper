-- +goose Up
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_ended_at ON runs(project_id, ended_at);

-- +goose Down
DROP INDEX IF EXISTS idx_runs_started_at;
DROP INDEX IF EXISTS idx_runs_ended_at;
