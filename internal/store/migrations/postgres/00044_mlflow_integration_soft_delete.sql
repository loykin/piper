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
-- 3. idx_mlflow_integrations_default was a plain (non-unique) index, so "at
--    most one Default=true integration per project" was only enforced by
--    application code (see mlflowRepo.CreateIntegration/UpdateIntegration's
--    "clear every other row's is_default in a transaction" logic), not the
--    database — two concurrent transactions could each successfully commit
--    a different row as Default=true. Replaced with a partial unique index
--    (also excluding soft-deleted rows, so a deleted row that happened to
--    be Default doesn't block a new one).
ALTER TABLE mlflow_integrations ADD COLUMN deleted_at TIMESTAMPTZ NULL;

ALTER TABLE mlflow_integrations DROP CONSTRAINT mlflow_integrations_project_id_name_key;
CREATE UNIQUE INDEX idx_mlflow_integrations_name ON mlflow_integrations(project_id, name) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_mlflow_integrations_default;
CREATE UNIQUE INDEX idx_mlflow_integrations_one_default ON mlflow_integrations(project_id) WHERE is_default = TRUE AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_mlflow_integrations_one_default;
CREATE INDEX idx_mlflow_integrations_default ON mlflow_integrations(project_id, is_default);
DROP INDEX IF EXISTS idx_mlflow_integrations_name;
ALTER TABLE mlflow_integrations ADD CONSTRAINT mlflow_integrations_project_id_name_key UNIQUE (project_id, name);
ALTER TABLE mlflow_integrations DROP COLUMN deleted_at;
