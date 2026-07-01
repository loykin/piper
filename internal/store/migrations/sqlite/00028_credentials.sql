-- +goose Up
CREATE TABLE IF NOT EXISTS credentials (
    project_id        TEXT     NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name              TEXT     NOT NULL,
    kind              TEXT     NOT NULL,
    endpoint          TEXT     NOT NULL DEFAULT '',
    keys_json         TEXT     NOT NULL DEFAULT '[]',
    disabled          BOOLEAN  NOT NULL DEFAULT FALSE,
    last_used_at      DATETIME NULL,
    last_tested_at    DATETIME NULL,
    last_test_ok      BOOLEAN  NULL,
    last_test_message TEXT     NOT NULL DEFAULT '',
    created_at        DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at        DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, name)
);

CREATE TABLE IF NOT EXISTS credential_values (
    project_id  TEXT     NOT NULL,
    name        TEXT     NOT NULL,
    version     INTEGER  NOT NULL,
    ciphertext  BLOB     NOT NULL,
    active      BOOLEAN  NOT NULL DEFAULT TRUE,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, name, version),
    FOREIGN KEY (project_id, name) REFERENCES credentials(project_id, name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_credential_values_active ON credential_values(project_id, name, active);

-- +goose Down
DROP TABLE IF EXISTS credential_values;
DROP TABLE IF EXISTS credentials;
