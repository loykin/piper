-- +goose Up
CREATE TABLE pipeline_templates_new (
    project_id  TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id          TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    version     INTEGER     NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    tags        TEXT        NOT NULL DEFAULT '[]',
    yaml        TEXT        NOT NULL DEFAULT '',
    snapshot_id TEXT        NOT NULL DEFAULT '',
    volume_id   TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, name, version)
);

INSERT INTO pipeline_templates_new
    (project_id, id, name, version, description, tags, yaml, snapshot_id, volume_id, created_at, updated_at)
SELECT
    v.project_id, v.id, t.name, v.version,
    COALESCE(t.description, ''), COALESCE(t.tags, '[]'),
    v.yaml, v.snapshot_id, v.volume_id,
    v.created_at, COALESCE(t.updated_at, v.created_at)
FROM pipeline_template_versions v
JOIN pipeline_templates t ON t.project_id = v.project_id AND t.id = v.template_id
ON CONFLICT DO NOTHING;

DROP TABLE pipeline_template_versions;
DROP TABLE pipeline_templates;
ALTER TABLE pipeline_templates_new RENAME TO pipeline_templates;

CREATE INDEX idx_pipeline_templates_name       ON pipeline_templates (project_id, name);
CREATE INDEX idx_pipeline_templates_created_at ON pipeline_templates (project_id, created_at DESC);

ALTER TABLE schedules DROP COLUMN IF EXISTS template_id;

-- +goose Down
ALTER TABLE schedules ADD COLUMN IF NOT EXISTS template_id TEXT NOT NULL DEFAULT '';

CREATE TABLE pipeline_templates (
    project_id  TEXT        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id          TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    tags        TEXT        NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, name)
);
CREATE TABLE pipeline_template_versions (
    project_id  TEXT        NOT NULL,
    id          TEXT        NOT NULL,
    template_id TEXT        NOT NULL,
    version     INTEGER     NOT NULL,
    yaml        TEXT        NOT NULL,
    snapshot_id TEXT        NOT NULL DEFAULT '',
    volume_id   TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, template_id, version),
    FOREIGN KEY (project_id, template_id) REFERENCES pipeline_templates(project_id, id) ON DELETE CASCADE
);
