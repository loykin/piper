-- +goose Up
ALTER TABLE notebook_volumes ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';

-- Backfill: set worker_id on bound volumes from their associated server's worker_id.
UPDATE notebook_volumes
SET worker_id = (
    SELECT worker_id FROM notebook_servers
    WHERE notebook_servers.volume_id = notebook_volumes.id
    LIMIT 1
)
WHERE status = 'bound';

-- +goose Down
-- SQLite does not support DROP COLUMN before 3.35.0; migration is not reversible.
SELECT 1;
