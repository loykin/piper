-- +goose Up
-- Re-key notebook volumes whose id equals their label.
-- Migration 00015 bootstrapped existing servers by setting volume id = server name,
-- which ties storage identity to workload name. This migration assigns proper UUIDs
-- so that volumes are truly independent of the servers that mount them.
--
-- SQLite has no built-in UUID function. We generate a v4-like UUID using randomblob:
--   lower(hex(randomblob(4)))-lower(hex(randomblob(2)))-4xxx-yxxx-lower(hex(randomblob(6)))
-- This is cryptographically random and UUID-v4 compatible for our purposes.

-- Step 1: Build a mapping table from old name-based id → new UUID.
CREATE TEMPORARY TABLE IF NOT EXISTS _vol_rekey (
    old_id TEXT NOT NULL,
    new_id TEXT NOT NULL
);

INSERT INTO _vol_rekey (old_id, new_id)
SELECT
    id AS old_id,
    lower(hex(randomblob(4))) || '-' ||
    lower(hex(randomblob(2))) || '-4' ||
    substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
    substr('89ab', (abs(random()) % 4) + 1, 1) ||
    substr(lower(hex(randomblob(2))), 1, 3) || '-' ||
    lower(hex(randomblob(6))) AS new_id
FROM notebook_volumes
WHERE id = label;  -- only rows that were migrated from server name, not already-UUID rows

-- Step 2: Insert new UUID-keyed volume records (copy of old rows with new id).
INSERT OR IGNORE INTO notebook_volumes (id, label, work_dir, status, created_at, updated_at)
SELECT r.new_id, v.label, v.work_dir, v.status, v.created_at, v.updated_at
FROM _vol_rekey r
JOIN notebook_volumes v ON v.id = r.old_id;

-- Step 3: Update notebook_servers to reference the new UUID volume ids.
UPDATE notebook_servers
SET volume_id = (
    SELECT new_id FROM _vol_rekey WHERE old_id = notebook_servers.volume_id
)
WHERE volume_id IN (SELECT old_id FROM _vol_rekey);

-- Step 4: Remove the old name-based volume records.
DELETE FROM notebook_volumes
WHERE id IN (SELECT old_id FROM _vol_rekey);

DROP TABLE IF EXISTS _vol_rekey;

-- +goose Down
-- Not reversible: UUID→name mapping is not stored after migration.
SELECT 1;
