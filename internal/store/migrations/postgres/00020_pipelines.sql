-- +goose Up
CREATE TABLE IF NOT EXISTS pipelines (
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    name        TEXT NOT NULL,
    yaml        TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    volume_id   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_pipelines_name ON pipelines (project_id, name);
CREATE INDEX IF NOT EXISTS idx_pipelines_created_at ON pipelines (project_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS pipelines;
