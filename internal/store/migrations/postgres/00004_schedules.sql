-- +goose Up
CREATE TABLE IF NOT EXISTS schedules (
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    name          TEXT NOT NULL,
    pipeline_yaml TEXT NOT NULL,
    schedule_type TEXT NOT NULL DEFAULT 'cron',
    cron_expr     TEXT NOT NULL DEFAULT '',
    params_json   TEXT NOT NULL DEFAULT '{}',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at   TIMESTAMPTZ,
    next_run_at   TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_schedules_next_run ON schedules(project_id, enabled, next_run_at);

-- +goose Down
DROP INDEX IF EXISTS idx_schedules_next_run;
DROP TABLE IF EXISTS schedules;
