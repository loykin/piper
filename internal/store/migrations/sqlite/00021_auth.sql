-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    system_admin  INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS project_members (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('viewer', 'member', 'admin')),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (project_id, user_id)
);

CREATE TABLE IF NOT EXISTS auth_sessions (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash TEXT UNIQUE NOT NULL,
    expires_at         TIMESTAMP NOT NULL,
    revoked_at         TIMESTAMP,
    created_at         TIMESTAMP NOT NULL,
    last_used_at       TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_user   ON auth_sessions(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_auth_sessions_user;
DROP INDEX IF EXISTS idx_project_members_user;
DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS project_members;
DROP TABLE IF EXISTS users;
