-- +goose Up
-- See the SQLite migration of the same number: without ON DELETE CASCADE, a
-- project that ever exported to MLflow could not be deleted, because its
-- integrations' outbox rows blocked the project → integration cascade.
ALTER TABLE integration_outbox_events
    DROP CONSTRAINT IF EXISTS integration_outbox_events_project_id_integration_id_fkey;
ALTER TABLE integration_outbox_events
    ADD CONSTRAINT integration_outbox_events_project_id_integration_id_fkey
    FOREIGN KEY (project_id, integration_id) REFERENCES mlflow_integrations(project_id, id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE integration_outbox_events
    DROP CONSTRAINT IF EXISTS integration_outbox_events_project_id_integration_id_fkey;
ALTER TABLE integration_outbox_events
    ADD CONSTRAINT integration_outbox_events_project_id_integration_id_fkey
    FOREIGN KEY (project_id, integration_id) REFERENCES mlflow_integrations(project_id, id);
