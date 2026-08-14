-- +goose Up
CREATE TABLE run_submissions (
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    run_id          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (project_id, idempotency_key)
);
CREATE INDEX idx_run_submissions_run ON run_submissions(project_id, run_id);

-- +goose Down
DROP INDEX IF EXISTS idx_run_submissions_run;
DROP TABLE IF EXISTS run_submissions;
