-- +goose Up
ALTER TABLE notebook_volumes ADD COLUMN IF NOT EXISTS worker_id TEXT NOT NULL DEFAULT '';

UPDATE notebook_volumes v
SET worker_id = s.worker_id
FROM notebook_servers s
WHERE s.volume_id = v.id AND v.status = 'bound';

-- +goose Down
ALTER TABLE notebook_volumes DROP COLUMN IF EXISTS worker_id;
