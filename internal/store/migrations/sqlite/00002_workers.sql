-- +goose Up
CREATE TABLE IF NOT EXISTS workers (
    id            TEXT PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',
    hostname      TEXT NOT NULL DEFAULT '',
    concurrency   INTEGER NOT NULL DEFAULT 1,
    status        TEXT NOT NULL DEFAULT 'online',
    in_flight     INTEGER NOT NULL DEFAULT 0,
    registered_at DATETIME NOT NULL,
    last_seen_at  DATETIME NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS workers;
