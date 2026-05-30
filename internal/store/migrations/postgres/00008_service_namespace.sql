-- +goose Up
ALTER TABLE services ADD COLUMN namespace TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN namespace;
