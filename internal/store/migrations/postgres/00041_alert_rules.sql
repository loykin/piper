-- +goose Up
CREATE TABLE alert_rules (
    id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    source TEXT NOT NULL,
    event_type TEXT NOT NULL DEFAULT '',
    when_expr TEXT NOT NULL DEFAULT '',
    metric_key TEXT NOT NULL DEFAULT '',
    condition_expr TEXT NOT NULL DEFAULT '',
    notify_json TEXT NOT NULL DEFAULT '[]',
    cooldown_seconds BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    last_matched_at TIMESTAMPTZ,
    last_attempted_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, name)
);
CREATE INDEX idx_alert_rules_enabled ON alert_rules(enabled, project_id);

-- +goose Down
DROP TABLE alert_rules;
