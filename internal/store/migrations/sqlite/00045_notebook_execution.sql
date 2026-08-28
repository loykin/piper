-- +goose Up
-- Phase 1 of docs/jupyter-mcp-execution.md: the domain tables backing
-- pkg/notebook/execution (KernelSession, NotebookExecution) plus a small
-- per-project override for notebook_execution.mcp_policy (design doc §9.3).
--
-- Neither table has a foreign key to notebook_servers (project_id, name):
-- a notebook server can be deleted and re-created under the same name, and
-- execution/kernel-session history must survive that (same reasoning as
-- notebook_history in 00038_notebook_history.sql, which also only
-- references projects(id) and keeps notebook name/path as plain columns).
--
-- No raw executed code, rich output, or Jupyter token/session/kernel IDs
-- are exposed outside this package — see model.go's
-- KernelSessionResponse/NotebookExecutionResponse doc comments. This
-- schema only stores what those DTOs (plus internal bookkeeping) need.
CREATE TABLE IF NOT EXISTS kernel_sessions (
    id                 TEXT      NOT NULL,
    project_id         TEXT      NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    notebook_name      TEXT      NOT NULL,
    notebook_path      TEXT      NOT NULL DEFAULT '',
    jupyter_session_id TEXT      NOT NULL DEFAULT '',
    kernel_id          TEXT      NOT NULL DEFAULT '',
    kernel_name        TEXT      NOT NULL DEFAULT '',
    status             TEXT      NOT NULL DEFAULT 'starting',
    created_by         TEXT      NOT NULL DEFAULT '',
    client_id          TEXT      NOT NULL DEFAULT '',
    last_activity_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at          TIMESTAMP NULL,
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_kernel_sessions_notebook ON kernel_sessions(project_id, notebook_name, created_at);
-- Backs the kernel_idle_ttl sweep (design doc §5.1/§11.1): find open
-- sessions whose last activity predates the TTL cutoff, across all projects.
CREATE INDEX IF NOT EXISTS idx_kernel_sessions_status_activity ON kernel_sessions(status, last_activity_at);

CREATE TABLE IF NOT EXISTS notebook_executions (
    id                TEXT      NOT NULL,
    project_id        TEXT      NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    notebook_name     TEXT      NOT NULL,
    notebook_path     TEXT      NOT NULL DEFAULT '',
    result_path       TEXT      NOT NULL DEFAULT '',
    kernel_session_id TEXT      NOT NULL DEFAULT '',
    kind              TEXT      NOT NULL DEFAULT 'notebook',
    status            TEXT      NOT NULL DEFAULT 'queued',
    requested_by      TEXT      NOT NULL DEFAULT '',
    client_id         TEXT      NOT NULL DEFAULT '',
    idempotency_key   TEXT      NOT NULL DEFAULT '',
    request_hash      TEXT      NOT NULL DEFAULT '',
    source_sha256     TEXT      NOT NULL DEFAULT '',
    base_content_hash TEXT      NOT NULL DEFAULT '',
    current_cell      INTEGER   NOT NULL DEFAULT 0,
    total_cells       INTEGER   NOT NULL DEFAULT 0,
    error_code        TEXT      NOT NULL DEFAULT '',
    error_message     TEXT      NOT NULL DEFAULT '',
    output_summary    BLOB      NULL,
    approved_by       TEXT      NOT NULL DEFAULT '',
    approved_at       TIMESTAMP NULL,
    denied_by         TEXT      NOT NULL DEFAULT '',
    denied_at         TIMESTAMP NULL,
    queued_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at        TIMESTAMP NULL,
    finished_at       TIMESTAMP NULL,
    updated_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_notebook_executions_notebook ON notebook_executions(project_id, notebook_name, queued_at);
-- Backs recovery at Piper startup (design doc §11.2): scan every
-- queued/running/cancelling execution across all projects in one query.
CREATE INDEX IF NOT EXISTS idx_notebook_executions_status ON notebook_executions(status);
-- Backs Idempotency-Key replay lookup (design doc §6/§7.3): same project +
-- actor + notebook + key must resolve to the same execution. Scoped to
-- non-empty keys only (most rows have none).
CREATE UNIQUE INDEX IF NOT EXISTS idx_notebook_executions_idempotency
    ON notebook_executions(project_id, notebook_name, requested_by, idempotency_key)
    WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS notebook_execution_policy (
    project_id TEXT      NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    mcp_policy TEXT      NOT NULL DEFAULT 'approval_required',
    updated_by TEXT      NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id)
);

-- +goose Down
DROP TABLE IF EXISTS notebook_execution_policy;
DROP TABLE IF EXISTS notebook_executions;
DROP TABLE IF EXISTS kernel_sessions;
