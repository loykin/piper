-- +goose Up
CREATE TABLE IF NOT EXISTS run_metrics (
    id          BIGSERIAL PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    run_id      TEXT NOT NULL,
    step_name   TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       DOUBLE PRECISION NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_metrics_run_step ON run_metrics(project_id, run_id, step_name);

-- +goose Down
DROP INDEX IF EXISTS idx_run_metrics_run_step;
DROP TABLE IF EXISTS run_metrics;
