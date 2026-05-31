-- +goose Up
ALTER TABLE notebook_servers ADD COLUMN image TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1; -- SQLite does not support DROP COLUMN in older versions; no-op
