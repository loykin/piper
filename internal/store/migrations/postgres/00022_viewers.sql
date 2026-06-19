-- +goose Up
CREATE TABLE IF NOT EXISTS viewers (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL DEFAULT '',
    type          TEXT NOT NULL,
    run_id        TEXT NOT NULL,
    step_name     TEXT NOT NULL,
    artifact      TEXT NOT NULL,
    status        TEXT NOT NULL,
    endpoint      TEXT NOT NULL DEFAULT '',
    pid           INTEGER NOT NULL DEFAULT 0,
    work_dir      TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_viewers_project ON viewers(project_id);
CREATE INDEX IF NOT EXISTS idx_viewers_run     ON viewers(project_id, run_id, step_name, artifact);

-- +goose Down
DROP INDEX IF EXISTS idx_viewers_run;
DROP INDEX IF EXISTS idx_viewers_project;
DROP TABLE IF EXISTS viewers;
