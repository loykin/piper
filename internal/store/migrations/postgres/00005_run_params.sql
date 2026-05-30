-- +goose Up
ALTER TABLE runs ADD COLUMN params_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE runs DROP COLUMN params_json;
