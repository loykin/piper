-- +goose Up
-- Stamps each Run and pipeline template version with the storage-identity
-- (see storageIdentity() in settings.go) that was live when its artifacts
-- were written, so a later legitimate backend migration (e.g. file -> s3, or
-- one bucket to another) can be told apart from silent data loss instead of
-- surfacing as an indistinguishable 404. Existing rows get '' (unknown /
-- predates this feature) — never treated as a mismatch, since there is no
-- baseline to compare against.
ALTER TABLE runs ADD COLUMN storage_backend TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_templates ADD COLUMN storage_backend TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns portably; keep storage_backend on rollback.
