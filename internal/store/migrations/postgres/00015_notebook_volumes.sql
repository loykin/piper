-- +goose Up
CREATE TABLE IF NOT EXISTS notebook_volumes (
    project_id TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id         TEXT        NOT NULL PRIMARY KEY,
    label      TEXT        NOT NULL DEFAULT '',
    work_dir   TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL DEFAULT 'bound',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notebook_volumes_project ON notebook_volumes(project_id);

ALTER TABLE notebook_servers ADD COLUMN IF NOT EXISTS volume_id TEXT NOT NULL DEFAULT '';

-- +goose Down
DROP INDEX IF EXISTS idx_notebook_volumes_project;
DROP TABLE IF EXISTS notebook_volumes;
