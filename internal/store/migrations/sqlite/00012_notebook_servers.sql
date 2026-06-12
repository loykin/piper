-- +goose Up
CREATE TABLE IF NOT EXISTS notebook_servers (
    project_id TEXT      NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT      NOT NULL,
    status     TEXT      NOT NULL DEFAULT 'stopped',
    endpoint   TEXT      NOT NULL DEFAULT '',
    pid        INTEGER   NOT NULL DEFAULT 0,
    work_dir   TEXT      NOT NULL DEFAULT '',
    token      TEXT      NOT NULL DEFAULT '',
    image      TEXT      NOT NULL DEFAULT '',
    namespace  TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, name)
);

-- +goose Down
DROP TABLE IF EXISTS notebook_servers;
