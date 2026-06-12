-- +goose Up
CREATE TABLE IF NOT EXISTS service_history (
    id          INTEGER   PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT      NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT      NOT NULL,
    run_id      TEXT      NOT NULL DEFAULT '',
    artifact    TEXT      NOT NULL DEFAULT '',
    status      TEXT      NOT NULL DEFAULT '',
    endpoint    TEXT      NOT NULL DEFAULT '',
    namespace   TEXT      NOT NULL DEFAULT '',
    pid         INTEGER   NOT NULL DEFAULT 0,
    yaml        TEXT      NOT NULL DEFAULT '',
    deployed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stopped_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_service_history_project ON service_history(project_id, stopped_at);

-- +goose Down
DROP TABLE IF EXISTS service_history;
