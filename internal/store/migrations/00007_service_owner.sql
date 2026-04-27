-- +goose Up
ALTER TABLE services ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns portably before 3.35; leave owner_id in place.
