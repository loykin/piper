-- +goose Up
CREATE TABLE IF NOT EXISTS pipelines (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    yaml        TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    volume_id   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pipelines_name ON pipelines (name);
CREATE INDEX IF NOT EXISTS idx_pipelines_created_at ON pipelines (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS pipelines;
