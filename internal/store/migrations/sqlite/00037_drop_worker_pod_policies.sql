-- +goose Up
-- worker_pod_policies (00023) was never read or written by any Go code —
-- a leftover from the removed remote-worker registration design.
DROP TABLE IF EXISTS worker_pod_policies;

-- +goose Down
CREATE TABLE IF NOT EXISTS worker_pod_policies (
    worker_id    TEXT     NOT NULL PRIMARY KEY,
    pod_template TEXT     NOT NULL,
    updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_by   TEXT     NOT NULL DEFAULT ''
);
