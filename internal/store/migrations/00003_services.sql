-- +goose Up
CREATE TABLE IF NOT EXISTS services (
    name       TEXT PRIMARY KEY,
    run_id     TEXT NOT NULL DEFAULT '',
    artifact   TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'stopped',
    endpoint   TEXT NOT NULL DEFAULT '',
    pid        INTEGER NOT NULL DEFAULT 0,
    yaml       TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS services;
