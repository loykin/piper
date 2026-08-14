-- +goose Up
CREATE TABLE project_mutations (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_status INTEGER NOT NULL DEFAULT 0,
    response_headers BYTEA,
    response_body BYTEA,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (project_id, idempotency_key)
);
-- +goose Down
DROP TABLE IF EXISTS project_mutations;
