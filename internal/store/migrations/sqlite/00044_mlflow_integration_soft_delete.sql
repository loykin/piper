-- +goose Up
-- Two fixes to mlflow_integrations, both concerning invariants the Go layer
-- alone can't enforce reliably:
--
-- 1. DeleteIntegration used to be a hard DELETE, and both mapping tables'
--    FK is ON DELETE CASCADE — so deleting an integration silently erased
--    its experiment/run mapping history, contradicting the documented
--    contract (design doc section 11.1: "Piper→MLflow mapping 보존...
--    완전한 purge는 별도 admin 작업"). Soft-delete via deleted_at instead:
--    the row (and therefore the FK target the mappings reference) survives,
--    only its Enabled/Default flags are cleared.
-- 2. The old UNIQUE(project_id, name) table constraint would also block
--    re-creating an integration under a name that belongs to a
--    soft-deleted row. Replaced with a partial unique index scoped to
--    deleted_at IS NULL, so a soft-deleted row's name is free to reuse.
-- 3. idx_mlflow_integrations_default was a plain (non-unique) index — see
--    the postgres migration's note — so "at most one Default=true
--    integration per project" was only enforced by application code, not
--    the database, and two concurrent transactions could each successfully
--    set a different row Default=true. Replaced with a partial unique
--    index (also excluding soft-deleted rows, so a deleted row that
--    happened to be Default doesn't block a new one).
--
-- SQLite can't drop an inline UNIQUE table constraint via ALTER TABLE, so
-- this rebuilds the table (existing project-scoped migration pattern, see
-- 00014_notebook_env.sql).
CREATE TABLE mlflow_integrations_v2 (
    id                         TEXT     NOT NULL,
    project_id                 TEXT     NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                       TEXT     NOT NULL,
    tracking_uri               TEXT     NOT NULL,
    credential_ref             TEXT     NOT NULL,
    enabled                    BOOLEAN  NOT NULL DEFAULT TRUE,
    is_default                 BOOLEAN  NOT NULL DEFAULT FALSE,
    export_pipelines           BOOLEAN  NOT NULL DEFAULT TRUE,
    export_notebook_executions BOOLEAN  NOT NULL DEFAULT FALSE,
    experiment_template        TEXT     NOT NULL DEFAULT '',
    artifact_mode              TEXT     NOT NULL DEFAULT 'reference',
    created_by                 TEXT     NOT NULL DEFAULT '',
    created_at                 DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at                 DATETIME NOT NULL DEFAULT (datetime('now')),
    deleted_at                 DATETIME NULL,
    PRIMARY KEY (project_id, id)
);
INSERT INTO mlflow_integrations_v2
SELECT id, project_id, name, tracking_uri, credential_ref, enabled, is_default,
       export_pipelines, export_notebook_executions, experiment_template,
       artifact_mode, created_by, created_at, updated_at, NULL
FROM mlflow_integrations;
DROP TABLE mlflow_integrations;
ALTER TABLE mlflow_integrations_v2 RENAME TO mlflow_integrations;

DROP INDEX IF EXISTS idx_mlflow_integrations_default;
CREATE UNIQUE INDEX idx_mlflow_integrations_name ON mlflow_integrations(project_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX idx_mlflow_integrations_one_default ON mlflow_integrations(project_id) WHERE is_default = TRUE AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_mlflow_integrations_one_default;
DROP INDEX IF EXISTS idx_mlflow_integrations_name;
ALTER TABLE mlflow_integrations DROP COLUMN deleted_at;
CREATE INDEX idx_mlflow_integrations_default ON mlflow_integrations(project_id, is_default);
