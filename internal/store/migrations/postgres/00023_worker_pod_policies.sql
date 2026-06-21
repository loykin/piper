-- +goose Up
CREATE TABLE IF NOT EXISTS worker_pod_policies (
    worker_id    TEXT        NOT NULL PRIMARY KEY,
    pod_template TEXT        NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by   TEXT        NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS worker_pod_policies;
