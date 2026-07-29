-- +goose Up
ALTER TABLE runs ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE service_history ADD COLUMN created_by TEXT NOT NULL DEFAULT '';

CREATE TABLE login_history (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL DEFAULT '',
    email          TEXT NOT NULL,
    success        INTEGER NOT NULL DEFAULT 0,
    failure_reason TEXT NOT NULL DEFAULT '',
    attempted_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_login_history_user ON login_history(user_id, attempted_at);
CREATE INDEX idx_login_history_email ON login_history(email, attempted_at);

-- +goose Down
DROP INDEX IF EXISTS idx_login_history_email;
DROP INDEX IF EXISTS idx_login_history_user;
DROP TABLE IF EXISTS login_history;
ALTER TABLE service_history DROP COLUMN created_by;
ALTER TABLE services DROP COLUMN created_by;
ALTER TABLE notebook_servers DROP COLUMN created_by;
ALTER TABLE runs DROP COLUMN created_by;
