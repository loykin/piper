-- +goose Up
CREATE TABLE IF NOT EXISTS pipeline_templates (
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, name)
);

CREATE TABLE IF NOT EXISTS pipeline_template_versions (
    project_id   TEXT NOT NULL,
    id           TEXT NOT NULL,
    template_id  TEXT NOT NULL,
    version      INTEGER NOT NULL,
    yaml         TEXT NOT NULL,
    snapshot_id  TEXT NOT NULL,
    volume_id    TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    created_by   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project_id, id),
    UNIQUE (project_id, template_id, version),
    FOREIGN KEY (project_id, template_id) REFERENCES pipeline_templates(project_id, id) ON DELETE CASCADE
);

INSERT OR IGNORE INTO pipeline_templates (project_id, id, name, created_at, updated_at)
SELECT project_id, MIN(id), name, MIN(created_at), MAX(created_at)
FROM pipelines
GROUP BY project_id, name;

INSERT OR IGNORE INTO pipeline_template_versions (project_id, id, template_id, version, yaml, snapshot_id, volume_id, created_at)
SELECT
    p.project_id,
    p.id,
    t.id,
    (
        SELECT COUNT(*)
        FROM pipelines p2
        WHERE p2.project_id = p.project_id
          AND p2.name = p.name
          AND (p2.created_at < p.created_at OR (p2.created_at = p.created_at AND p2.id <= p.id))
    ),
    p.yaml,
    p.snapshot_id,
    p.volume_id,
    p.created_at
FROM pipelines p
JOIN pipeline_templates t ON t.project_id = p.project_id AND t.name = p.name;

ALTER TABLE schedules ADD COLUMN template_id TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN template_version_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_pipeline_templates_name ON pipeline_templates (project_id, name);
CREATE INDEX IF NOT EXISTS idx_pipeline_template_versions_template ON pipeline_template_versions (project_id, template_id, version DESC);
CREATE INDEX IF NOT EXISTS idx_pipeline_template_versions_created_at ON pipeline_template_versions (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_schedules_template_version ON schedules (project_id, template_version_id);

DROP INDEX IF EXISTS idx_pipelines_created_at;
DROP INDEX IF EXISTS idx_pipelines_name;
DROP TABLE IF EXISTS pipelines;

-- +goose Down
DROP INDEX IF EXISTS idx_schedules_template_version;
DROP INDEX IF EXISTS idx_pipeline_template_versions_created_at;
DROP INDEX IF EXISTS idx_pipeline_template_versions_template;
DROP INDEX IF EXISTS idx_pipeline_templates_name;

CREATE TABLE IF NOT EXISTS pipelines_restored (
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    name        TEXT NOT NULL,
    yaml        TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    volume_id   TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, id)
);

INSERT OR IGNORE INTO pipelines_restored (project_id, id, name, yaml, snapshot_id, volume_id, created_at)
SELECT v.project_id, v.id, t.name, v.yaml, v.snapshot_id, v.volume_id, v.created_at
FROM pipeline_template_versions v
JOIN pipeline_templates t ON t.project_id = v.project_id AND t.id = v.template_id;

DROP TABLE IF EXISTS pipelines;
ALTER TABLE pipelines_restored RENAME TO pipelines;
CREATE INDEX IF NOT EXISTS idx_pipelines_name ON pipelines (project_id, name);
CREATE INDEX IF NOT EXISTS idx_pipelines_created_at ON pipelines (project_id, created_at DESC);

DROP TABLE IF EXISTS pipeline_template_versions;
DROP TABLE IF EXISTS pipeline_templates;
