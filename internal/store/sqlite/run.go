package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/piper/piper/pkg/run"
)

type runRepo struct{ db *sql.DB }

func NewRunRepo(db *sql.DB) run.Repository { return &runRepo{db: db} }

func (r *runRepo) Create(ctx context.Context, row *run.Run) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO runs (id, schedule_id, owner_id, pipeline_name, status, started_at, scheduled_at, pipeline_yaml)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.ScheduleID, row.OwnerID, row.PipelineName, row.Status,
		row.StartedAt, row.ScheduledAt, row.PipelineYAML,
	)
	return err
}

func (r *runRepo) Get(ctx context.Context, id string) (*run.Run, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, schedule_id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at FROM runs WHERE id=?`, id,
	)
	return scanRun(row)
}

func (r *runRepo) List(ctx context.Context, filter run.RunFilter) ([]*run.Run, error) {
	query := `SELECT id, schedule_id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at FROM runs`
	var args []any
	var where []string
	if filter.OwnerID != "" {
		where = append(where, "owner_id=?")
		args = append(args, filter.OwnerID)
	}
	if filter.PipelineName != "" {
		where = append(where, "pipeline_name=?")
		args = append(args, filter.PipelineName)
	}
	if filter.ScheduleID != "" {
		where = append(where, "schedule_id=?")
		args = append(args, filter.ScheduleID)
	}
	if filter.Status != "" {
		where = append(where, "status=?")
		args = append(args, filter.Status)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY started_at DESC"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*run.Run, 0)
	for rows.Next() {
		v, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *runRepo) UpdateStatus(ctx context.Context, id, status string, endedAt *time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE runs SET status=?, ended_at=? WHERE id=?`, status, endedAt, id)
	return err
}

func (r *runRepo) MarkRunning(ctx context.Context, id string, startedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE runs SET status='running', started_at=? WHERE id=?`, startedAt, id)
	return err
}

func (r *runRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM runs WHERE id=?`, id)
	return err
}

func (r *runRepo) GetLatestSuccessful(ctx context.Context, pipelineName string) (*run.Run, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, schedule_id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at
		 FROM runs WHERE pipeline_name=? AND status='success' ORDER BY started_at DESC LIMIT 1`,
		pipelineName,
	)
	v, err := scanRun(row)
	if err != nil && strings.Contains(err.Error(), "no rows") {
		return nil, nil
	}
	return v, err
}

type scanner interface{ Scan(dest ...any) error }

func scanRun(s scanner) (*run.Run, error) {
	var v run.Run
	var endedAt, scheduledAt sql.NullTime
	if err := s.Scan(&v.ID, &v.ScheduleID, &v.OwnerID, &v.PipelineName, &v.Status,
		&v.StartedAt, &endedAt, &scheduledAt); err != nil {
		return nil, fmt.Errorf("scan run: %w", err)
	}
	if endedAt.Valid {
		v.EndedAt = &endedAt.Time
	}
	if scheduledAt.Valid {
		v.ScheduledAt = &scheduledAt.Time
	}
	return &v, nil
}
