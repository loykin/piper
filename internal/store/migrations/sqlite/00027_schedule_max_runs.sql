-- +goose Up
ALTER TABLE schedules ADD COLUMN max_runs INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE schedules DROP COLUMN max_runs;
