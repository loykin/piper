package postgres

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/schedule"
)

type scheduleRepo struct{ db *sqlx.DB }

func NewScheduleRepo(db *sqlx.DB) schedule.Repository { return &scheduleRepo{db: db} }

const scheduleSelectCols = `project_id, id, name, pipeline_yaml, template_version_id, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type`

func (r *scheduleRepo) Create(ctx context.Context, sc *schedule.Schedule) error {
	q := r.db.Rebind(`INSERT INTO schedules (project_id, id, name, pipeline_yaml, template_version_id, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		sc.ProjectID, sc.ID, sc.Name, sc.PipelineYAML, sc.VersionID, sc.CronExpr, sc.ParamsJSON,
		sc.Enabled, sc.LastRunAt, sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt, sc.ScheduleType,
	)
	return err
}

func (r *scheduleRepo) Get(ctx context.Context, projectID, id string) (*schedule.Schedule, error) {
	var sc schedule.Schedule
	q := r.db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE project_id=? AND id=?`)
	err := r.db.GetContext(ctx, &sc, q, projectID, id)
	if err != nil {
		return nil, err
	}
	if sc.ScheduleType == "" {
		sc.ScheduleType = "cron"
	}
	return &sc, nil
}

func (r *scheduleRepo) List(ctx context.Context, projectID string) ([]*schedule.Schedule, error) {
	var rows []*schedule.Schedule
	q := r.db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE project_id=? ORDER BY created_at DESC`)
	err := r.db.SelectContext(ctx, &rows, q, projectID)
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

func (r *scheduleRepo) ListEnabled(ctx context.Context) ([]*schedule.Schedule, error) {
	var rows []*schedule.Schedule
	q := r.db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE enabled=? ORDER BY created_at ASC`)
	err := r.db.SelectContext(ctx, &rows, q, true)
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

func (r *scheduleRepo) ClaimRun(ctx context.Context, projectID, id string, expectedAt, lastRunAt, nextRunAt time.Time) (bool, error) {
	q := r.db.Rebind(`UPDATE schedules SET last_run_at=?, next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=? AND next_run_at=?`)
	res, err := r.db.ExecContext(ctx, q, lastRunAt, nextRunAt, time.Now().UTC(), projectID, id, true, expectedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *scheduleRepo) AdvanceNextRun(ctx context.Context, projectID, id string, expectedAt, nextRunAt time.Time) (bool, error) {
	q := r.db.Rebind(`UPDATE schedules SET next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=? AND next_run_at=?`)
	res, err := r.db.ExecContext(ctx, q, nextRunAt, time.Now().UTC(), projectID, id, true, expectedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *scheduleRepo) ClaimOneShotRun(ctx context.Context, projectID, id string, expectedAt, lastRunAt time.Time) (bool, error) {
	q := r.db.Rebind(`UPDATE schedules SET enabled=?, last_run_at=?, next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=? AND next_run_at=?`)
	res, err := r.db.ExecContext(ctx, q, false, lastRunAt, lastRunAt, time.Now().UTC(), projectID, id, true, expectedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *scheduleRepo) SetEnabled(ctx context.Context, projectID, id string, enabled bool) error {
	q := r.db.Rebind(`UPDATE schedules SET enabled=?, updated_at=? WHERE project_id=? AND id=?`)
	_, err := r.db.ExecContext(ctx, q, enabled, time.Now().UTC(), projectID, id)
	return err
}

func (r *scheduleRepo) Delete(ctx context.Context, projectID, id string) error {
	q := r.db.Rebind(`DELETE FROM schedules WHERE project_id=? AND id=?`)
	_, err := r.db.ExecContext(ctx, q, projectID, id)
	return err
}
