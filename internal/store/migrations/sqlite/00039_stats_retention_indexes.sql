-- +goose Up
CREATE INDEX IF NOT EXISTS idx_logs_retention ON logs(ts, id);
CREATE INDEX IF NOT EXISTS idx_run_metrics_retention ON run_metrics(recorded_at, id);

-- +goose Down
DROP INDEX IF EXISTS idx_run_metrics_retention;
DROP INDEX IF EXISTS idx_logs_retention;
