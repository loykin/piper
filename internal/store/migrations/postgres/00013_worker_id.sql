-- +goose Up
ALTER TABLE services ADD COLUMN IF NOT EXISTS worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers ADD COLUMN IF NOT EXISTS worker_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN IF EXISTS worker_id;
ALTER TABLE notebook_servers DROP COLUMN IF EXISTS worker_id;
