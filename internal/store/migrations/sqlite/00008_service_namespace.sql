-- +goose Up
ALTER TABLE services ADD COLUMN namespace TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns portably before 3.35; leave namespace in place.
