-- +goose Up
CREATE TABLE IF NOT EXISTS services (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    run_id     TEXT NOT NULL DEFAULT '',
    artifact   TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'stopped',
    endpoint   TEXT NOT NULL DEFAULT '',
    pid        INTEGER NOT NULL DEFAULT 0,
    yaml       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (project_id, name)
);

-- +goose Down
DROP TABLE IF EXISTS services;
