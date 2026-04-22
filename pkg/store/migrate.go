package store

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// migration defines a versioned database migration.
type migration struct {
	version int
	desc    string
	up      string
}

// migrations is the ordered list of migrations to apply.
// Add new schema changes here only — never modify existing entries.
var migrations = []migration{
	{
		version: 1,
		desc:    "create core tables",
		up: `
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
			CREATE INDEX IF NOT EXISTS idx_logs_step ON logs(run_id, step_name);
			CREATE INDEX IF NOT EXISTS idx_steps_run ON steps(run_id);
			CREATE INDEX IF NOT EXISTS idx_runs_schedule ON runs(schedule_id);
		`,
	},
	{
		version: 3,
		desc:    "create workers and agents tables",
		up: `
			CREATE TABLE IF NOT EXISTS workers (
				id            TEXT PRIMARY KEY,
				label         TEXT NOT NULL DEFAULT '',
				hostname      TEXT NOT NULL DEFAULT '',
				concurrency   INTEGER NOT NULL DEFAULT 1,
				status        TEXT NOT NULL DEFAULT 'online',
				in_flight     INTEGER NOT NULL DEFAULT 0,
				registered_at DATETIME NOT NULL,
				last_seen_at  DATETIME NOT NULL
			);
			CREATE TABLE IF NOT EXISTS agents (
				id         TEXT PRIMARY KEY,
				task_id    TEXT NOT NULL,
				run_id     TEXT NOT NULL DEFAULT '',
				step_name  TEXT NOT NULL DEFAULT '',
				status     TEXT NOT NULL DEFAULT 'pending',
				started_at DATETIME,
				ended_at   DATETIME,
				error      TEXT
			);
			CREATE INDEX IF NOT EXISTS idx_agents_task ON agents(task_id);
			CREATE INDEX IF NOT EXISTS idx_agents_run  ON agents(run_id, step_name);
		`,
	},
	{
		version: 5,
		desc:    "create services table",
		up: `
			CREATE TABLE IF NOT EXISTS services (
				name       TEXT PRIMARY KEY,
				run_id     TEXT NOT NULL DEFAULT '',
				artifact   TEXT NOT NULL DEFAULT '',
				status     TEXT NOT NULL DEFAULT 'stopped',
				endpoint   TEXT NOT NULL DEFAULT '',
				pid        INTEGER NOT NULL DEFAULT 0,
				yaml       TEXT NOT NULL DEFAULT '',
				created_at DATETIME NOT NULL,
				updated_at DATETIME NOT NULL
			);
		`,
	},
	{
		version: 6,
		desc:    "create schedules table",
		up: `
			CREATE TABLE IF NOT EXISTS schedules (
				id            TEXT PRIMARY KEY,
				name          TEXT NOT NULL,
				owner_id      TEXT NOT NULL DEFAULT '',
				pipeline_yaml TEXT NOT NULL,
				schedule_type TEXT NOT NULL DEFAULT 'cron',
				cron_expr     TEXT NOT NULL DEFAULT '',
				params_json   TEXT NOT NULL DEFAULT '{}',
				enabled       INTEGER NOT NULL DEFAULT 1,
				last_run_at   DATETIME,
				next_run_at   DATETIME NOT NULL,
				created_at    DATETIME NOT NULL,
				updated_at    DATETIME NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_schedules_next_run ON schedules(enabled, next_run_at);
		`,
	},
}

// migrate applies any pending migrations in order.
func migrate(db *sql.DB) error {
	// Create the migration tracking table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			desc       TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Query the currently applied versions
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	// Run pending migrations
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}

		slog.Info("applying migration", "version", m.version, "desc", m.desc)

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration %d: begin tx: %w", m.version, err)
		}

		if _, err := tx.Exec(m.up); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
		}

		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, desc) VALUES (?, ?)`,
			m.version, m.desc,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: record: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: commit: %w", m.version, err)
		}

		slog.Info("migration applied", "version", m.version)
	}

	return nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
