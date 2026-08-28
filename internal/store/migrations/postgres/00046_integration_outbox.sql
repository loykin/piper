-- +goose Up
-- Durable integration outbox (docs/mlflow-tracking-adapter.md section 6.3):
-- an ordered, at-least-once delivery queue between a Piper domain lifecycle
-- event (a pipeline run starting/finishing) and an external integration
-- adapter (MLflow today). pkg/integration/outbox owns the generic
-- model/repository/dispatcher; this table backs it.
--
-- The FK targets mlflow_integrations specifically (the only integration
-- table that exists yet) rather than a generic "integrations" table — a
-- future non-MLflow integration adapter would need either its own outbox
-- table or a polymorphic integration reference; deliberately not solved
-- here since MLflow is the only consumer in this phase.
--
-- Migration number 00046 (not 00045): a second, concurrently-running agent
-- is using 00045 for unrelated work — see the MLflow Phase 1 task brief.
CREATE TABLE IF NOT EXISTS integration_outbox_events (
    id               TEXT        NOT NULL PRIMARY KEY,
    integration_id   TEXT        NOT NULL,
    project_id       TEXT        NOT NULL,
    aggregate_type   TEXT        NOT NULL,
    aggregate_id     TEXT        NOT NULL,
    sequence         BIGINT      NOT NULL,
    event_type       TEXT        NOT NULL,
    payload_json     BYTEA       NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending',
    attempts         INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner      TEXT        NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ NULL,
    last_error_code  TEXT        NOT NULL DEFAULT '',
    last_error       TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at     TIMESTAMPTZ NULL,
    FOREIGN KEY (project_id, integration_id) REFERENCES mlflow_integrations(project_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_outbox_unique
    ON integration_outbox_events(integration_id, aggregate_type, aggregate_id, sequence, event_type);

-- Claim query: due/reclaimable rows for one integration, cheapest first.
CREATE INDEX IF NOT EXISTS idx_integration_outbox_claim
    ON integration_outbox_events(integration_id, status, next_attempt_at);

-- Per-aggregate ordering gate (MIN(sequence) among not-yet-terminal rows).
CREATE INDEX IF NOT EXISTS idx_integration_outbox_aggregate
    ON integration_outbox_events(integration_id, aggregate_type, aggregate_id, status, sequence);

-- +goose Down
DROP TABLE IF EXISTS integration_outbox_events;
