CREATE TABLE IF NOT EXISTS service_history (
    id          INTEGER   PRIMARY KEY AUTOINCREMENT,
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
