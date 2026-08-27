-- +goose Up
CREATE TABLE alert_rules (
    id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    name TEXT NOT NULL,
    source TEXT NOT NULL,
    event_type TEXT NOT NULL DEFAULT '',
    when_expr TEXT NOT NULL DEFAULT '',
    metric_key TEXT NOT NULL DEFAULT '',
    condition_expr TEXT NOT NULL DEFAULT '',
    notify_json TEXT NOT NULL DEFAULT '[]',
    cooldown_seconds INTEGER NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL DEFAULT '',
    last_matched_at DATETIME,
    last_attempted_at DATETIME,
    last_success_at DATETIME,
    last_error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX idx_alert_rules_enabled ON alert_rules(enabled, project_id);

-- +goose Down
DROP TABLE alert_rules;
