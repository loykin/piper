-- +goose Up
ALTER TABLE schedules ADD COLUMN IF NOT EXISTS max_runs INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE schedules DROP COLUMN IF EXISTS max_runs;
