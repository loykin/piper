package store

import (
	"database/sql"
	"fmt"
	"time"
)

type Run struct {
	ID           string     `json:"id"`
	PipelineName string     `json:"pipeline_name"`
	Status       string     `json:"status"` // running | success | failed
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	PipelineYAML string     `json:"pipeline_yaml,omitempty"`
}

func (s *Store) CreateRun(r *Run) error {
	_, err := s.db.Exec(
		`INSERT INTO runs (id, pipeline_name, status, started_at, pipeline_yaml)
		 VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.PipelineName, r.Status, r.StartedAt, r.PipelineYAML,
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

func (s *Store) GetRun(id string) (*Run, error) {
	row := s.db.QueryRow(
		`SELECT id, pipeline_name, status, started_at, ended_at FROM runs WHERE id=?`, id,
	)
	return scanRun(row)
}

func (s *Store) ListRuns() ([]*Run, error) {
	rows, err := s.db.Query(
		`SELECT id, pipeline_name, status, started_at, ended_at FROM runs ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*Run
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
	var endedAt sql.NullTime
	if err := s.Scan(&r.ID, &r.PipelineName, &r.Status, &r.StartedAt, &endedAt); err != nil {
		return nil, fmt.Errorf("scan run: %w", err)
	}
	if endedAt.Valid {
		r.EndedAt = &endedAt.Time
	}
	return &r, nil
}
