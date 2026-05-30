-- +goose Up
ALTER TABLE services ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN owner_id;
