-- +goose Up
ALTER TABLE runs ADD COLUMN experiment TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE runs DROP COLUMN experiment;
