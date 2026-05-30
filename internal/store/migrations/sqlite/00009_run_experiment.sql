-- +goose Up
ALTER TABLE runs ADD COLUMN experiment TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns portably; keep experiment on rollback.
