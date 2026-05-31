-- +goose Up
-- Re-key legacy notebook volumes whose id equals their label to proper UUIDs.
-- Migration 00015 bootstrapped existing servers by setting volume id = server name,
-- which ties storage identity to workload name. This migration assigns proper UUIDs
-- so that volumes are truly independent of the servers that mount them.
DO $$
DECLARE
    r RECORD;
    new_id TEXT;
BEGIN
    FOR r IN SELECT id, label FROM notebook_volumes WHERE id = label LOOP
        new_id := gen_random_uuid()::text;
        INSERT INTO notebook_volumes (id, label, work_dir, status, created_at, updated_at)
        SELECT new_id, label, work_dir, status, created_at, updated_at
        FROM notebook_volumes WHERE id = r.id;
        UPDATE notebook_servers SET volume_id = new_id WHERE volume_id = r.id;
        DELETE FROM notebook_volumes WHERE id = r.id;
    END LOOP;
END $$;

-- +goose Down
SELECT 1;
