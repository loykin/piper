-- +goose Up
ALTER TABLE workers ADD COLUMN version TEXT NOT NULL DEFAULT '';
ALTER TABLE workers ADD COLUMN capabilities TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns portably; keep version and capabilities on rollback.
