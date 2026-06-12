-- +goose Up
CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    schedule_id   TEXT NOT NULL DEFAULT '',
    pipeline_name TEXT NOT NULL,
    status        TEXT NOT NULL,
    started_at    TIMESTAMPTZ NOT NULL,
    ended_at      TIMESTAMPTZ,
    scheduled_at  TIMESTAMPTZ,
    pipeline_yaml TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project_id, id)
);

CREATE TABLE IF NOT EXISTS steps (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    run_id     TEXT NOT NULL,
    step_name  TEXT NOT NULL,
    status     TEXT NOT NULL,
    started_at TIMESTAMPTZ,
    ended_at   TIMESTAMPTZ,
    error      TEXT,
    attempts   INTEGER DEFAULT 0,
    PRIMARY KEY (project_id, run_id, step_name)
);

CREATE TABLE IF NOT EXISTS logs (
    id        BIGSERIAL PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    run_id    TEXT NOT NULL,
    step_name TEXT NOT NULL,
    ts        TIMESTAMPTZ NOT NULL,
    stream    TEXT NOT NULL,
    line      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_logs_step ON logs(project_id, run_id, step_name);
CREATE INDEX IF NOT EXISTS idx_steps_run ON steps(project_id, run_id);
CREATE INDEX IF NOT EXISTS idx_runs_schedule ON runs(project_id, schedule_id);

-- +goose Down
DROP INDEX IF EXISTS idx_runs_schedule;
DROP INDEX IF EXISTS idx_steps_run;
DROP INDEX IF EXISTS idx_logs_step;
DROP TABLE IF EXISTS logs;
DROP TABLE IF EXISTS steps;
DROP TABLE IF EXISTS runs;
DROP TABLE IF EXISTS projects;
