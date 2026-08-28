-- +goose Up
CREATE TABLE IF NOT EXISTS mlflow_integrations (
    id                         TEXT        NOT NULL,
    project_id                 TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                       TEXT        NOT NULL,
    tracking_uri               TEXT        NOT NULL,
    credential_ref             TEXT        NOT NULL,
    enabled                    BOOLEAN     NOT NULL DEFAULT TRUE,
    is_default                 BOOLEAN     NOT NULL DEFAULT FALSE,
    export_pipelines           BOOLEAN     NOT NULL DEFAULT TRUE,
    export_notebook_executions BOOLEAN     NOT NULL DEFAULT FALSE,
    experiment_template        TEXT        NOT NULL DEFAULT '',
    artifact_mode              TEXT        NOT NULL DEFAULT 'reference',
    created_by                 TEXT        NOT NULL DEFAULT '',
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS idx_mlflow_integrations_default ON mlflow_integrations(project_id, is_default);

CREATE TABLE IF NOT EXISTS mlflow_experiment_links (
    integration_id       TEXT        NOT NULL,
    project_id           TEXT        NOT NULL,
    piper_group_key      TEXT        NOT NULL,
    mlflow_experiment_id TEXT        NOT NULL,
    mlflow_name          TEXT        NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, project_id, piper_group_key),
    FOREIGN KEY (project_id, integration_id) REFERENCES mlflow_integrations(project_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS mlflow_run_links (
    integration_id       TEXT        NOT NULL,
    project_id           TEXT        NOT NULL,
    source_type          TEXT        NOT NULL,
    source_id            TEXT        NOT NULL,
    mlflow_experiment_id TEXT        NOT NULL DEFAULT '',
    mlflow_run_id        TEXT        NOT NULL DEFAULT '',
    mlflow_run_url       TEXT        NOT NULL DEFAULT '',
    sync_status          TEXT        NOT NULL DEFAULT 'pending',
    last_sequence        BIGINT      NOT NULL DEFAULT 0,
    last_error_code      TEXT        NOT NULL DEFAULT '',
    last_error_message   TEXT        NOT NULL DEFAULT '',
    last_synced_at       TIMESTAMPTZ NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, project_id, source_type, source_id),
    FOREIGN KEY (project_id, integration_id) REFERENCES mlflow_integrations(project_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_mlflow_run_links_status ON mlflow_run_links(project_id, sync_status, updated_at);

-- +goose Down
DROP TABLE IF EXISTS mlflow_run_links;
DROP TABLE IF EXISTS mlflow_experiment_links;
DROP TABLE IF EXISTS mlflow_integrations;
