-- +goose Up
-- integration_outbox_events referenced mlflow_integrations without an ON
-- DELETE action, so deleting a project — which cascades to its MLflow
-- integrations — failed with "FOREIGN KEY constraint failed" as soon as the
-- integration had ever enqueued an event (delivered rows are kept too). Any
-- project that had exported a run to MLflow could never be deleted. Events
-- for an integration that no longer exists have nowhere to go, so they are
-- removed with it. (Integration deletion through the API is a soft delete and
-- never hits this cascade; only project deletion hard-deletes the row.)
--
-- SQLite can't alter an inline FOREIGN KEY, so the table is rebuilt with every
-- column and index unchanged apart from the ON DELETE CASCADE.
CREATE TABLE integration_outbox_events_v2 (
    id               TEXT     NOT NULL PRIMARY KEY,
    integration_id   TEXT     NOT NULL,
    project_id       TEXT     NOT NULL,
    aggregate_type   TEXT     NOT NULL,
    aggregate_id     TEXT     NOT NULL,
    sequence         INTEGER  NOT NULL,
    event_type       TEXT     NOT NULL,
    payload_json     BLOB     NOT NULL,
    status           TEXT     NOT NULL DEFAULT 'pending',
    attempts         INTEGER  NOT NULL DEFAULT 0,
    next_attempt_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    lease_owner      TEXT     NOT NULL DEFAULT '',
    lease_expires_at DATETIME NULL,
    last_error_code  TEXT     NOT NULL DEFAULT '',
    last_error       TEXT     NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT (datetime('now')),
    delivered_at     DATETIME NULL,
    FOREIGN KEY (project_id, integration_id) REFERENCES mlflow_integrations(project_id, id) ON DELETE CASCADE
);
INSERT INTO integration_outbox_events_v2 SELECT * FROM integration_outbox_events;
DROP TABLE integration_outbox_events;
ALTER TABLE integration_outbox_events_v2 RENAME TO integration_outbox_events;

CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_outbox_unique
    ON integration_outbox_events(integration_id, aggregate_type, aggregate_id, sequence, event_type);
CREATE INDEX IF NOT EXISTS idx_integration_outbox_claim
    ON integration_outbox_events(integration_id, status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_integration_outbox_aggregate
    ON integration_outbox_events(integration_id, aggregate_type, aggregate_id, status, sequence);

-- +goose Down
CREATE TABLE integration_outbox_events_v1 (
    id               TEXT     NOT NULL PRIMARY KEY,
    integration_id   TEXT     NOT NULL,
    project_id       TEXT     NOT NULL,
    aggregate_type   TEXT     NOT NULL,
    aggregate_id     TEXT     NOT NULL,
    sequence         INTEGER  NOT NULL,
    event_type       TEXT     NOT NULL,
    payload_json     BLOB     NOT NULL,
    status           TEXT     NOT NULL DEFAULT 'pending',
    attempts         INTEGER  NOT NULL DEFAULT 0,
    next_attempt_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    lease_owner      TEXT     NOT NULL DEFAULT '',
    lease_expires_at DATETIME NULL,
    last_error_code  TEXT     NOT NULL DEFAULT '',
    last_error       TEXT     NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT (datetime('now')),
    delivered_at     DATETIME NULL,
    FOREIGN KEY (project_id, integration_id) REFERENCES mlflow_integrations(project_id, id)
);
INSERT INTO integration_outbox_events_v1 SELECT * FROM integration_outbox_events;
DROP TABLE integration_outbox_events;
ALTER TABLE integration_outbox_events_v1 RENAME TO integration_outbox_events;
CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_outbox_unique
    ON integration_outbox_events(integration_id, aggregate_type, aggregate_id, sequence, event_type);
CREATE INDEX IF NOT EXISTS idx_integration_outbox_claim
    ON integration_outbox_events(integration_id, status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_integration_outbox_aggregate
    ON integration_outbox_events(integration_id, aggregate_type, aggregate_id, status, sequence);
