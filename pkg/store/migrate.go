package store

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// migration은 버전별 마이그레이션 정의
type migration struct {
	version int
	desc    string
	up      string
}

// migrations는 순서대로 적용되는 마이그레이션 목록
// 새 스키마 변경은 여기에 추가만 하면 됨 — 기존 항목 수정 금지
var migrations = []migration{
	{
		version: 1,
		desc:    "create runs, steps, logs tables",
		up: `
			CREATE TABLE IF NOT EXISTS runs (
				id            TEXT PRIMARY KEY,
				pipeline_name TEXT NOT NULL,
				status        TEXT NOT NULL,
				started_at    DATETIME NOT NULL,
				ended_at      DATETIME,
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
		`,
	},
	// 새 마이그레이션은 여기에 추가
	// {
	//     version: 2,
	//     desc:    "add tags column to runs",
	//     up:      `ALTER TABLE runs ADD COLUMN tags TEXT DEFAULT ''`,
	// },
}

// migrate는 미적용 마이그레이션을 순서대로 실행한다
func migrate(db *sql.DB) error {
	// 마이그레이션 추적 테이블 생성
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			desc       TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// 현재 적용된 버전 조회
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	// 미적용 마이그레이션 실행
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
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
		}

		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, desc) VALUES (?, ?)`,
			m.version, m.desc,
		); err != nil {
			tx.Rollback()
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
	defer rows.Close()

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
