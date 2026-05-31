-- +goose Up
ALTER TABLE notebook_servers ADD COLUMN image TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1; -- no-op
