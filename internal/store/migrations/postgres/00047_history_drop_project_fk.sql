-- +goose Up
-- See the sqlite migration of the same name for the full rationale:
-- notebook_history/service_history are append-only audit logs, the same
-- category as logs/metrics (internal/logstore, pkg/statsstore) which already
-- live outside the relational schema and are purged via explicit
-- project.Handler.WithBeforeDelete calls (serve.go), never a DB-level FK.
-- Dropping the FK treats project_id as a plain tag; retention.
-- NotebookHistoryTTL/ServiceHistoryTTL (internal/retention) is now the only
-- lifecycle policy for these rows.
ALTER TABLE notebook_history DROP CONSTRAINT notebook_history_project_id_fkey;
ALTER TABLE service_history DROP CONSTRAINT service_history_project_id_fkey;

-- +goose Down
ALTER TABLE notebook_history ADD CONSTRAINT notebook_history_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE service_history ADD CONSTRAINT service_history_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
