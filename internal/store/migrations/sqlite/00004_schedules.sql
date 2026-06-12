-- +goose Up
CREATE TABLE IF NOT EXISTS schedules (
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    name          TEXT NOT NULL,
    pipeline_yaml TEXT NOT NULL,
    schedule_type TEXT NOT NULL DEFAULT 'cron',
    cron_expr     TEXT NOT NULL DEFAULT '',
    params_json   TEXT NOT NULL DEFAULT '{}',
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_run_at   DATETIME,
    next_run_at   DATETIME NOT NULL,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_schedules_next_run ON schedules(project_id, enabled, next_run_at);

-- +goose Down
DROP INDEX IF EXISTS idx_schedules_next_run;
DROP TABLE IF EXISTS schedules;
