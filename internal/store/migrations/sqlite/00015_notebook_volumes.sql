-- +goose Up
CREATE TABLE IF NOT EXISTS notebook_volumes (
    id         TEXT      NOT NULL PRIMARY KEY,
    label      TEXT      NOT NULL DEFAULT '',
    work_dir   TEXT      NOT NULL DEFAULT '',
    status     TEXT      NOT NULL DEFAULT 'bound',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE notebook_servers ADD COLUMN volume_id TEXT NOT NULL DEFAULT '';

-- Migrate existing servers: create a volume record per server using name as volume ID.
INSERT OR IGNORE INTO notebook_volumes (id, label, work_dir, status, created_at, updated_at)
SELECT name, name, work_dir, 'bound', created_at, updated_at
FROM notebook_servers WHERE work_dir != '';

UPDATE notebook_servers SET volume_id = name WHERE work_dir != '';

-- +goose Down
DROP TABLE IF EXISTS notebook_volumes;
