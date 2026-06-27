-- +goose Up
ALTER TABLE pipeline_templates ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';

-- +goose Down
-- SQLite does not support DROP COLUMN on older versions; leave the column in place.
