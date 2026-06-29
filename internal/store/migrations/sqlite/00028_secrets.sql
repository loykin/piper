-- +goose Up
CREATE TABLE IF NOT EXISTS secrets (
    project_id   TEXT     NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name         TEXT     NOT NULL,
    type         TEXT     NOT NULL,
    provider     TEXT     NOT NULL,
    keys_json    TEXT     NOT NULL DEFAULT '[]',
    disabled     BOOLEAN  NOT NULL DEFAULT FALSE,
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    last_used_at DATETIME NULL,
    PRIMARY KEY (project_id, name)
);

CREATE TABLE IF NOT EXISTS secret_values (
    project_id  TEXT     NOT NULL,
    name        TEXT     NOT NULL,
    version     INTEGER  NOT NULL,
    ciphertext  BLOB     NOT NULL,
    active      BOOLEAN  NOT NULL DEFAULT TRUE,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, name, version),
    FOREIGN KEY (project_id, name) REFERENCES secrets(project_id, name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_secret_values_active ON secret_values(project_id, name, active);

-- +goose Down
DROP TABLE IF EXISTS secret_values;
DROP TABLE IF EXISTS secrets;

