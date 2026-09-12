-- +goose Up
-- notebook_history/service_history are append-only audit logs of past
-- lifecycle events, not live relational entities — the same category as
-- logs/metrics (internal/logstore, pkg/statsstore), which already live
-- outside the relational schema entirely and are purged by explicit
-- project.Handler.WithBeforeDelete calls (serve.go), never by a DB-level FK.
-- These two tables were modeled with `project_id ... REFERENCES projects(id)
-- ON DELETE CASCADE` instead, which meant deleting a project silently wiped
-- its entire notebook/service history the moment FK enforcement was actually
-- turned on (_foreign_keys=on) — an accidental side effect nobody decided on,
-- since the FK bought no integrity benefit AppendHistory didn't already have
-- (its project_id always comes from an already-live notebook_servers/
-- services row, never user input) and no code joins these tables against
-- projects. Dropping the FK — not RESTRICT, not soft-delete — instead treats
-- project_id as a plain tag, matching how logstore/statsstore already treat
-- it: retention.NotebookHistoryTTL/ServiceHistoryTTL (internal/retention) is
-- now the only lifecycle policy for these rows, independent of whether the
-- project they reference still exists.
--
-- SQLite can't drop an inline REFERENCES clause via ALTER TABLE, so this
-- rebuilds both tables (existing project-scoped migration pattern, see
-- 00044_mlflow_integration_soft_delete.sql), preserving every column/index
-- as-is other than the FK itself.
CREATE TABLE notebook_history_v2 (
    id          INTEGER   PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT      NOT NULL,
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
INSERT INTO notebook_history_v2
SELECT id, project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, image, yaml, created_by, deployed_at, stopped_at
FROM notebook_history;
DROP TABLE notebook_history;
ALTER TABLE notebook_history_v2 RENAME TO notebook_history;
CREATE INDEX IF NOT EXISTS idx_notebook_history_project ON notebook_history(project_id, stopped_at);

CREATE TABLE service_history_v2 (
    id          INTEGER   PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT      NOT NULL,
    name        TEXT      NOT NULL,
    run_id      TEXT      NOT NULL DEFAULT '',
    artifact    TEXT      NOT NULL DEFAULT '',
    status      TEXT      NOT NULL DEFAULT '',
    endpoint    TEXT      NOT NULL DEFAULT '',
    namespace   TEXT      NOT NULL DEFAULT '',
    pid         INTEGER   NOT NULL DEFAULT 0,
    yaml        TEXT      NOT NULL DEFAULT '',
    deployed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stopped_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by  TEXT      NOT NULL DEFAULT ''
);
INSERT INTO service_history_v2
SELECT id, project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, deployed_at, stopped_at, created_by
FROM service_history;
DROP TABLE service_history;
ALTER TABLE service_history_v2 RENAME TO service_history;
CREATE INDEX IF NOT EXISTS idx_service_history_project ON service_history(project_id, stopped_at);

-- +goose Down
CREATE TABLE notebook_history_v2 (
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
INSERT INTO notebook_history_v2
SELECT id, project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, image, yaml, created_by, deployed_at, stopped_at
FROM notebook_history;
DROP TABLE notebook_history;
ALTER TABLE notebook_history_v2 RENAME TO notebook_history;
CREATE INDEX IF NOT EXISTS idx_notebook_history_project ON notebook_history(project_id, stopped_at);

CREATE TABLE service_history_v2 (
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
    stopped_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by  TEXT      NOT NULL DEFAULT ''
);
INSERT INTO service_history_v2
SELECT id, project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, deployed_at, stopped_at, created_by
FROM service_history;
DROP TABLE service_history;
ALTER TABLE service_history_v2 RENAME TO service_history;
CREATE INDEX IF NOT EXISTS idx_service_history_project ON service_history(project_id, stopped_at);
