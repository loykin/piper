package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/piper/piper/pkg/schedule"
)

type scheduleRepo struct{ db *sql.DB }

func NewScheduleRepo(db *sql.DB) schedule.Repository { return &scheduleRepo{db: db} }

func (r *scheduleRepo) Create(ctx context.Context, sc *schedule.Schedule) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO schedules (id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sc.ID, sc.Name, sc.OwnerID, sc.PipelineYAML, sc.CronExpr, sc.ParamsJSON,
		boolToInt(sc.Enabled), sc.LastRunAt, sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt, sc.ScheduleType,
	)
	return err
}

func (r *scheduleRepo) Get(ctx context.Context, id string) (*schedule.Schedule, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type
		 FROM schedules WHERE id=?`, id)
	return scanSchedule(row)
}

func (r *scheduleRepo) List(ctx context.Context) ([]*schedule.Schedule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type
		 FROM schedules ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*schedule.Schedule, 0)
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (r *scheduleRepo) ListDue(ctx context.Context, now time.Time) ([]*schedule.Schedule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type
		 FROM schedules WHERE enabled=1 AND next_run_at <= ? ORDER BY next_run_at ASC`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*schedule.Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (r *scheduleRepo) UpdateRun(ctx context.Context, id string, lastRunAt, nextRunAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE schedules SET last_run_at=?, next_run_at=?, updated_at=? WHERE id=?`,
		lastRunAt, nextRunAt, time.Now().UTC(), id)
	return err
}

func (r *scheduleRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE schedules SET enabled=?, updated_at=? WHERE id=?`,
		boolToInt(enabled), time.Now().UTC(), id)
	return err
}

func (r *scheduleRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM schedules WHERE id=?`, id)
	return err
}

func scanSchedule(s interface{ Scan(dest ...any) error }) (*schedule.Schedule, error) {
	var out schedule.Schedule
	var enabledInt int
	var lastRunAt sql.NullTime
	if err := s.Scan(&out.ID, &out.Name, &out.OwnerID, &out.PipelineYAML, &out.CronExpr,
		&out.ParamsJSON, &enabledInt, &lastRunAt, &out.NextRunAt,
		&out.CreatedAt, &out.UpdatedAt, &out.ScheduleType); err != nil {
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
