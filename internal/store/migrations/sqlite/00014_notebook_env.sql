-- +goose Up
-- Rebuild notebook_servers: add env, remove image/namespace (k8s legacy).
CREATE TABLE notebook_servers_v2 (
    name       TEXT      PRIMARY KEY,
    status     TEXT      NOT NULL DEFAULT 'stopped',
    env        TEXT      NOT NULL DEFAULT '',
    endpoint   TEXT      NOT NULL DEFAULT '',
    pid        INTEGER   NOT NULL DEFAULT 0,
    work_dir   TEXT      NOT NULL DEFAULT '',
    token      TEXT      NOT NULL DEFAULT '',
    worker_id  TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO notebook_servers_v2 (name, status, endpoint, pid, work_dir, token, worker_id, created_at, updated_at)
SELECT name, status, endpoint, pid, work_dir, token, worker_id, created_at, updated_at
FROM notebook_servers;
DROP TABLE notebook_servers;
ALTER TABLE notebook_servers_v2 RENAME TO notebook_servers;

-- +goose Down
ALTER TABLE notebook_servers ADD COLUMN image     TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers ADD COLUMN namespace TEXT NOT NULL DEFAULT '';
