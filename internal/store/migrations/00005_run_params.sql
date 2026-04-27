-- +goose Up
ALTER TABLE runs ADD COLUMN params_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
-- SQLite cannot drop columns portably; keep params_json on rollback.
