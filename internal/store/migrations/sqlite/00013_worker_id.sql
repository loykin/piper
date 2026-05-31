-- +goose Up
ALTER TABLE services ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; leave as-is on rollback
