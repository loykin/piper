-- +goose Up
CREATE TABLE federation_members (
    home_id              TEXT NOT NULL,
    id                   TEXT NOT NULL,
    enabled              INTEGER NOT NULL DEFAULT 1,
    status               TEXT NOT NULL DEFAULT 'offline',
    last_connected_at    TIMESTAMP,
    last_disconnected_at TIMESTAMP,
    created_at           TIMESTAMP NOT NULL,
    updated_at           TIMESTAMP NOT NULL,
    PRIMARY KEY (home_id, id)
);
CREATE INDEX idx_federation_members_status ON federation_members(home_id, enabled, status);

CREATE TABLE federation_audit_events (
    id         TEXT PRIMARY KEY,
    home_id    TEXT NOT NULL,
    type       TEXT NOT NULL,
    member_id  TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    actor_id   TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_federation_audit_home_created ON federation_audit_events(home_id, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_federation_audit_home_created;
DROP TABLE IF EXISTS federation_audit_events;
DROP INDEX IF EXISTS idx_federation_members_status;
DROP TABLE IF EXISTS federation_members;
