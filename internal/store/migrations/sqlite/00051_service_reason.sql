-- +goose Up
-- reason: why a service reached its current status (health check timeout,
-- the process's last output before exiting, a crash-looping container's
-- waiting reason), so a failed deployment explains itself in the record,
-- the API and the UI instead of only in server logs. Same as
-- notebook_servers.reason (00049).
ALTER TABLE services ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE service_history ADD COLUMN reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE service_history DROP COLUMN reason;
ALTER TABLE services DROP COLUMN reason;
