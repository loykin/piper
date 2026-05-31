-- +goose Up
CREATE TABLE IF NOT EXISTS notebook_servers (
    name       TEXT      PRIMARY KEY,
    status     TEXT      NOT NULL DEFAULT 'stopped',
    endpoint   TEXT      NOT NULL DEFAULT '',
    pid        INTEGER   NOT NULL DEFAULT 0,
    work_dir   TEXT      NOT NULL DEFAULT '',
    token      TEXT      NOT NULL DEFAULT '',
    image      TEXT      NOT NULL DEFAULT '',
    namespace  TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS notebook_servers;
