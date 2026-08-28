-- +goose Up
-- Phase 1 of docs/jupyter-mcp-execution.md: the domain tables backing
-- pkg/notebook/execution (KernelSession, NotebookExecution) plus a small
-- per-project override for notebook_execution.mcp_policy (design doc §9.3).
-- See the SQLite migration of the same number for the full rationale
-- (no FK to notebook_servers, no raw code/token/session storage).
CREATE TABLE IF NOT EXISTS kernel_sessions (
    id                 TEXT        NOT NULL,
    project_id         TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    notebook_name      TEXT        NOT NULL,
    notebook_path      TEXT        NOT NULL DEFAULT '',
    jupyter_session_id TEXT        NOT NULL DEFAULT '',
    kernel_id          TEXT        NOT NULL DEFAULT '',
    kernel_name        TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL DEFAULT 'starting',
    created_by         TEXT        NOT NULL DEFAULT '',
    client_id          TEXT        NOT NULL DEFAULT '',
    last_activity_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at          TIMESTAMPTZ NULL,
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_kernel_sessions_notebook ON kernel_sessions(project_id, notebook_name, created_at);
CREATE INDEX IF NOT EXISTS idx_kernel_sessions_status_activity ON kernel_sessions(status, last_activity_at);

CREATE TABLE IF NOT EXISTS notebook_executions (
    id                TEXT        NOT NULL,
    project_id        TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    notebook_name     TEXT        NOT NULL,
    notebook_path     TEXT        NOT NULL DEFAULT '',
    result_path       TEXT        NOT NULL DEFAULT '',
    kernel_session_id TEXT        NOT NULL DEFAULT '',
    kind              TEXT        NOT NULL DEFAULT 'notebook',
    status            TEXT        NOT NULL DEFAULT 'queued',
    requested_by      TEXT        NOT NULL DEFAULT '',
    client_id         TEXT        NOT NULL DEFAULT '',
    idempotency_key   TEXT        NOT NULL DEFAULT '',
    request_hash      TEXT        NOT NULL DEFAULT '',
    source_sha256     TEXT        NOT NULL DEFAULT '',
    base_content_hash TEXT        NOT NULL DEFAULT '',
    current_cell      INTEGER     NOT NULL DEFAULT 0,
    total_cells       INTEGER     NOT NULL DEFAULT 0,
    error_code        TEXT        NOT NULL DEFAULT '',
    error_message     TEXT        NOT NULL DEFAULT '',
    output_summary    BYTEA       NULL,
    approved_by       TEXT        NOT NULL DEFAULT '',
    approved_at       TIMESTAMPTZ NULL,
    denied_by         TEXT        NOT NULL DEFAULT '',
    denied_at         TIMESTAMPTZ NULL,
    queued_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ NULL,
    finished_at       TIMESTAMPTZ NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_notebook_executions_notebook ON notebook_executions(project_id, notebook_name, queued_at);
CREATE INDEX IF NOT EXISTS idx_notebook_executions_status ON notebook_executions(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notebook_executions_idempotency
    ON notebook_executions(project_id, notebook_name, requested_by, idempotency_key)
    WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS notebook_execution_policy (
    project_id TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    mcp_policy TEXT        NOT NULL DEFAULT 'approval_required',
    updated_by TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id)
);

-- +goose Down
DROP TABLE IF EXISTS notebook_execution_policy;
DROP TABLE IF EXISTS notebook_executions;
DROP TABLE IF EXISTS kernel_sessions;
