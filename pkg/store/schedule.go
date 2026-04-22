package store

import (
	"database/sql"
	"time"
)

type Schedule struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	OwnerID      string     `json:"owner_id,omitempty"`
	PipelineYAML string     `json:"pipeline_yaml"`
	ScheduleType string     `json:"schedule_type"` // "cron" | "once"
	CronExpr     string     `json:"cron_expr,omitempty"`
	Enabled      bool       `json:"enabled"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
	NextRunAt    time.Time  `json:"next_run_at"`
	ParamsJSON   string     `json:"params_json,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (s *Store) CreateSchedule(sc *Schedule) error {
	_, err := s.db.Exec(
		`INSERT INTO schedules (id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sc.ID, sc.Name, sc.OwnerID, sc.PipelineYAML, sc.CronExpr, sc.ParamsJSON, boolToInt(sc.Enabled), sc.LastRunAt, sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt, sc.ScheduleType,
	)
	return err
}

func (s *Store) GetSchedule(id string) (*Schedule, error) {
	row := s.db.QueryRow(
		`SELECT id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type
     FROM schedules WHERE id=?`, id,
	)
	return scanSchedule(row)
}

func (s *Store) ListSchedules() ([]*Schedule, error) {
	rows, err := s.db.Query(
		`SELECT id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type
     FROM schedules
     ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*Schedule, 0) // never nil → always marshals as []
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) ListDueSchedules(now time.Time) ([]*Schedule, error) {
	rows, err := s.db.Query(
		`SELECT id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type
     FROM schedules
     WHERE enabled=1 AND next_run_at <= ?
     ORDER BY next_run_at ASC`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) UpdateScheduleRun(id string, lastRunAt, nextRunAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE schedules
     SET last_run_at=?, next_run_at=?, updated_at=?
     WHERE id=?`,
		lastRunAt, nextRunAt, time.Now().UTC(), id,
	)
	return err
}

func (s *Store) SetScheduleEnabled(id string, enabled bool) error {
	_, err := s.db.Exec(
		`UPDATE schedules SET enabled=?, updated_at=? WHERE id=?`,
		boolToInt(enabled), time.Now().UTC(), id,
	)
	return err
}

func (s *Store) DeleteSchedule(id string) error {
	_, err := s.db.Exec(`DELETE FROM schedules WHERE id=?`, id)
	return err
}

func scanSchedule(sc interface{ Scan(dest ...any) error }) (*Schedule, error) {
	var out Schedule
	var enabledInt int
	var lastRunAt sql.NullTime
	if err := sc.Scan(
		&out.ID,
		&out.Name,
		&out.OwnerID,
		&out.PipelineYAML,
		&out.CronExpr,
		&out.ParamsJSON,
		&enabledInt,
		&lastRunAt,
		&out.NextRunAt,
		&out.CreatedAt,
		&out.UpdatedAt,
		&out.ScheduleType,
	); err != nil {
		return nil, err
	}
	out.Enabled = enabledInt == 1
	if lastRunAt.Valid {
		out.LastRunAt = &lastRunAt.Time
	}
	if out.ScheduleType == "" {
		out.ScheduleType = "cron"
	}
	return &out, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
