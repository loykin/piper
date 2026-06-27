-- +goose Up
ALTER TABLE pipeline_templates ADD COLUMN IF NOT EXISTS tags TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE pipeline_templates DROP COLUMN IF EXISTS tags;
