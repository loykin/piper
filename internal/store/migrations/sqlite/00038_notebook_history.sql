-- +goose Up
CREATE TABLE IF NOT EXISTS notebook_history (
    id          INTEGER   PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT      NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT      NOT NULL,
    status      TEXT      NOT NULL DEFAULT '',
    env         TEXT      NOT NULL DEFAULT '',
    endpoint    TEXT      NOT NULL DEFAULT '',
    pid         INTEGER   NOT NULL DEFAULT 0,
    work_dir    TEXT      NOT NULL DEFAULT '',
    runtime_id  TEXT      NOT NULL DEFAULT '',
    volume_id   TEXT      NOT NULL DEFAULT '',
    image       TEXT      NOT NULL DEFAULT '',
    yaml        TEXT      NOT NULL DEFAULT '',
    created_by  TEXT      NOT NULL DEFAULT '',
    deployed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stopped_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_notebook_history_project ON notebook_history(project_id, stopped_at);

-- +goose Down
DROP TABLE IF EXISTS notebook_history;
