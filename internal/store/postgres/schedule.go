package postgres

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/schedule"
)

type scheduleRepo struct{ db *sqlx.DB }

func NewScheduleRepo(db *sqlx.DB) schedule.Repository { return &scheduleRepo{db: db} }

const scheduleSelectCols = `id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type`

func (r *scheduleRepo) Create(ctx context.Context, sc *schedule.Schedule) error {
	q := r.db.Rebind(`INSERT INTO schedules (id, name, owner_id, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		sc.ID, sc.Name, sc.OwnerID, sc.PipelineYAML, sc.CronExpr, sc.ParamsJSON,
		sc.Enabled, sc.LastRunAt, sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt, sc.ScheduleType,
	)
	return err
}

func (r *scheduleRepo) Get(ctx context.Context, id string) (*schedule.Schedule, error) {
	var sc schedule.Schedule
	q := r.db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE id=?`)
	err := r.db.GetContext(ctx, &sc, q, id)
	if err != nil {
		return nil, err
	}
	if sc.ScheduleType == "" {
		sc.ScheduleType = "cron"
	}
	return &sc, nil
}

func (r *scheduleRepo) List(ctx context.Context) ([]*schedule.Schedule, error) {
	var rows []*schedule.Schedule
	err := r.db.SelectContext(ctx, &rows,
		`SELECT `+scheduleSelectCols+` FROM schedules ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	for _, sc := range rows {
		if sc.ScheduleType == "" {
			sc.ScheduleType = "cron"
		}
	}
	return rows, nil
}

func (r *scheduleRepo) ListDue(ctx context.Context, now time.Time) ([]*schedule.Schedule, error) {
	var rows []*schedule.Schedule
	q := r.db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE enabled=? AND next_run_at <= ? ORDER BY next_run_at ASC`)
	err := r.db.SelectContext(ctx, &rows, q, true, now)
	if err != nil {
		return nil, err
	}
	for _, sc := range rows {
		if sc.ScheduleType == "" {
			sc.ScheduleType = "cron"
		}
	}
	return rows, nil
}

func (r *scheduleRepo) UpdateRun(ctx context.Context, id string, lastRunAt, nextRunAt time.Time) error {
	q := r.db.Rebind(`UPDATE schedules SET last_run_at=?, next_run_at=?, updated_at=? WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, lastRunAt, nextRunAt, time.Now().UTC(), id)
	return err
}

func (r *scheduleRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	q := r.db.Rebind(`UPDATE schedules SET enabled=?, updated_at=? WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, enabled, time.Now().UTC(), id)
	return err
}

func (r *scheduleRepo) Delete(ctx context.Context, id string) error {
	q := r.db.Rebind(`DELETE FROM schedules WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, id)
	return err
}
