-- +goose Up
CREATE TABLE IF NOT EXISTS worker_pod_policies (
    worker_id    TEXT     NOT NULL PRIMARY KEY,
    pod_template TEXT     NOT NULL,
    updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_by   TEXT     NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS worker_pod_policies;
