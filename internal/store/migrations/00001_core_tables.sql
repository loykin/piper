-- +goose Up
CREATE TABLE IF NOT EXISTS runs (
    id            TEXT PRIMARY KEY,
    schedule_id   TEXT NOT NULL DEFAULT '',
    owner_id      TEXT NOT NULL DEFAULT '',
    pipeline_name TEXT NOT NULL,
    status        TEXT NOT NULL,
    started_at    DATETIME NOT NULL,
    ended_at      DATETIME,
    scheduled_at  DATETIME,
    pipeline_yaml TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS steps (
    run_id     TEXT NOT NULL,
    step_name  TEXT NOT NULL,
    status     TEXT NOT NULL,
    started_at DATETIME,
    ended_at   DATETIME,
    error      TEXT,
    attempts   INTEGER DEFAULT 0,
    PRIMARY KEY (run_id, step_name)
);

CREATE TABLE IF NOT EXISTS logs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id    TEXT NOT NULL,
    step_name TEXT NOT NULL,
    ts        DATETIME NOT NULL,
    stream    TEXT NOT NULL,
    line      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_logs_step  ON logs(run_id, step_name);
CREATE INDEX IF NOT EXISTS idx_steps_run  ON steps(run_id);
CREATE INDEX IF NOT EXISTS idx_runs_schedule ON runs(schedule_id);

-- +goose Down
DROP INDEX IF EXISTS idx_runs_schedule;
DROP INDEX IF EXISTS idx_steps_run;
DROP INDEX IF EXISTS idx_logs_step;
DROP TABLE IF EXISTS logs;
DROP TABLE IF EXISTS steps;
DROP TABLE IF EXISTS runs;
