-- +goose Up
ALTER TABLE workers ADD COLUMN version TEXT NOT NULL DEFAULT '';
ALTER TABLE workers ADD COLUMN capabilities TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE workers DROP COLUMN capabilities;
ALTER TABLE workers DROP COLUMN version;
