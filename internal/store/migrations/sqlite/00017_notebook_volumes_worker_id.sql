-- +goose Up
ALTER TABLE notebook_volumes ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';

-- Backfill worker_id from the bound server (new PK: project_id, name).
UPDATE notebook_volumes
SET worker_id = (
    SELECT s.worker_id FROM notebook_servers s
    WHERE s.volume_id = notebook_volumes.id
    LIMIT 1
)
WHERE status = 'bound';

-- +goose Down
-- SQLite does not support DROP COLUMN before 3.35.0.
SELECT 1;
