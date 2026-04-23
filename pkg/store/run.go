package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Run struct {
	ID           string     `json:"id"`
	ScheduleID   string     `json:"schedule_id,omitempty"`
	OwnerID      string     `json:"owner_id,omitempty"`
	PipelineName string     `json:"pipeline_name"`
	Status       string     `json:"status"` // running | success | failed
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	ScheduledAt  *time.Time `json:"scheduled_at,omitempty"`
	PipelineYAML string     `json:"pipeline_yaml,omitempty"`
}

func (s *Store) CreateRun(r *Run) error {
	_, err := s.db.Exec(
		`INSERT INTO runs (id, schedule_id, owner_id, pipeline_name, status, started_at, scheduled_at, pipeline_yaml)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ScheduleID, r.OwnerID, r.PipelineName, r.Status, r.StartedAt, r.ScheduledAt, r.PipelineYAML,
	)
	return err
}

func (s *Store) UpdateRunStatus(id, status string, endedAt *time.Time) error {
	_, err := s.db.Exec(
		`UPDATE runs SET status=?, ended_at=? WHERE id=?`,
		status, endedAt, id,
	)
	return err
}

func (s *Store) MarkRunRunning(id string, startedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE runs SET status='running', started_at=? WHERE id=?`,
		startedAt, id,
	)
	return err
}

func (s *Store) GetRun(id string) (*Run, error) {
	row := s.db.QueryRow(
		`SELECT id, schedule_id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at FROM runs WHERE id=?`, id,
	)
	return scanRun(row)
}

// RunFilter holds filter conditions for listing runs.
type RunFilter struct {
	OwnerID      string
	PipelineName string
	ScheduleID   string
	Status       string
}

func (s *Store) ListRuns(filter ...RunFilter) ([]*Run, error) {
	query := `SELECT id, schedule_id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at FROM runs`
	var args []any
	var where []string

	if len(filter) > 0 {
		f := filter[0]
		if f.OwnerID != "" {
			where = append(where, "owner_id=?")
			args = append(args, f.OwnerID)
		}
		if f.PipelineName != "" {
			where = append(where, "pipeline_name=?")
			args = append(args, f.PipelineName)
		}
		if f.ScheduleID != "" {
			where = append(where, "schedule_id=?")
			args = append(args, f.ScheduleID)
		}
		if f.Status != "" {
			where = append(where, "status=?")
			args = append(args, f.Status)
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY started_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	runs := make([]*Run, 0)
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(s scanner) (*Run, error) {
	var r Run
	var endedAt, scheduledAt sql.NullTime
	if err := s.Scan(&r.ID, &r.ScheduleID, &r.OwnerID, &r.PipelineName, &r.Status, &r.StartedAt, &endedAt, &scheduledAt); err != nil {
		return nil, fmt.Errorf("scan run: %w", err)
	}
	if endedAt.Valid {
		r.EndedAt = &endedAt.Time
	}
	if scheduledAt.Valid {
		r.ScheduledAt = &scheduledAt.Time
	}
	return &r, nil
}

// DeleteRun removes a run record along with its steps and logs from the DB.
func (s *Store) DeleteRun(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM logs WHERE run_id=?`,
		`DELETE FROM steps WHERE run_id=?`,
		`DELETE FROM runs WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
