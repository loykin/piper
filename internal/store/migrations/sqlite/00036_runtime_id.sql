-- +goose Up
ALTER TABLE notebook_servers RENAME COLUMN worker_id TO runtime_id;
ALTER TABLE notebook_volumes RENAME COLUMN worker_id TO runtime_id;
ALTER TABLE services RENAME COLUMN worker_id TO runtime_id;

-- +goose Down
ALTER TABLE services RENAME COLUMN runtime_id TO worker_id;
ALTER TABLE notebook_volumes RENAME COLUMN runtime_id TO worker_id;
ALTER TABLE notebook_servers RENAME COLUMN runtime_id TO worker_id;
