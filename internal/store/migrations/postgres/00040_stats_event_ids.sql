-- +goose Up
ALTER TABLE logs ADD COLUMN event_id TEXT NOT NULL DEFAULT '';
ALTER TABLE run_metrics ADD COLUMN event_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_logs_event_id ON logs(event_id) WHERE event_id <> '';
CREATE UNIQUE INDEX idx_run_metrics_event_id ON run_metrics(event_id) WHERE event_id <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_run_metrics_event_id;
DROP INDEX IF EXISTS idx_logs_event_id;
ALTER TABLE run_metrics DROP COLUMN event_id;
ALTER TABLE logs DROP COLUMN event_id;
