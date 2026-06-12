package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/schedule"
)

type scheduleRepo struct{ db *sqlx.DB }

func NewScheduleRepo(db *sqlx.DB) schedule.Repository { return &scheduleRepo{db: db} }

// scheduleRow is the DB scan target; maps int-stored enabled to bool.
type scheduleRow struct {
	ProjectID    string     `db:"project_id"`
	ID           string     `db:"id"`
	Name         string     `db:"name"`
	PipelineYAML string     `db:"pipeline_yaml"`
	ScheduleType string     `db:"schedule_type"`
	CronExpr     string     `db:"cron_expr"`
	EnabledInt   int        `db:"enabled"`
	LastRunAt    *time.Time `db:"last_run_at"`
	NextRunAt    time.Time  `db:"next_run_at"`
	ParamsJSON   string     `db:"params_json"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

func (s scheduleRow) toSchedule() *schedule.Schedule {
	sc := &schedule.Schedule{
		ProjectID:    s.ProjectID,
		ID:           s.ID,
		Name:         s.Name,
		PipelineYAML: s.PipelineYAML,
		ScheduleType: s.ScheduleType,
		CronExpr:     s.CronExpr,
		Enabled:      s.EnabledInt == 1,
		LastRunAt:    s.LastRunAt,
		NextRunAt:    s.NextRunAt,
		ParamsJSON:   s.ParamsJSON,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
	}
	if sc.ScheduleType == "" {
		sc.ScheduleType = "cron"
	}
	return sc
}

const scheduleSelectCols = `project_id, id, name, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type`

func (r *scheduleRepo) Create(ctx context.Context, sc *schedule.Schedule) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO schedules (project_id, id, name, pipeline_yaml, cron_expr, params_json, enabled, last_run_at, next_run_at, created_at, updated_at, schedule_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sc.ProjectID, sc.ID, sc.Name, sc.PipelineYAML, sc.CronExpr, sc.ParamsJSON,
		boolToInt(sc.Enabled), sc.LastRunAt, sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt, sc.ScheduleType,
	)
	return err
}

func (r *scheduleRepo) Get(ctx context.Context, projectID, id string) (*schedule.Schedule, error) {
	var row scheduleRow
	err := r.db.GetContext(ctx, &row,
		`SELECT `+scheduleSelectCols+` FROM schedules WHERE project_id=? AND id=?`, projectID, id)
	if err != nil {
		return nil, err
	}
	return row.toSchedule(), nil
}

func (r *scheduleRepo) List(ctx context.Context, projectID string) ([]*schedule.Schedule, error) {
	var rows []scheduleRow
	err := r.db.SelectContext(ctx, &rows,
		`SELECT `+scheduleSelectCols+` FROM schedules WHERE project_id=? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]*schedule.Schedule, len(rows))
	for i, row := range rows {
		out[i] = row.toSchedule()
	}
	return out, nil
}

func (r *scheduleRepo) ListDue(ctx context.Context, now time.Time) ([]*schedule.Schedule, error) {
	var rows []scheduleRow
	err := r.db.SelectContext(ctx, &rows,
		`SELECT `+scheduleSelectCols+` FROM schedules WHERE enabled=1 AND next_run_at <= ? ORDER BY next_run_at ASC`, now)
	if err != nil {
		return nil, err
	}
	out := make([]*schedule.Schedule, len(rows))
	for i, row := range rows {
		out[i] = row.toSchedule()
	}
	return out, nil
}

func (r *scheduleRepo) UpdateRun(ctx context.Context, projectID, id string, lastRunAt, nextRunAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE schedules SET last_run_at=?, next_run_at=?, updated_at=? WHERE project_id=? AND id=?`,
		lastRunAt, nextRunAt, time.Now().UTC(), projectID, id)
	return err
}

func (r *scheduleRepo) SetEnabled(ctx context.Context, projectID, id string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE schedules SET enabled=?, updated_at=? WHERE project_id=? AND id=?`,
		boolToInt(enabled), time.Now().UTC(), projectID, id)
	return err
}

func (r *scheduleRepo) Delete(ctx context.Context, projectID, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM schedules WHERE project_id=? AND id=?`, projectID, id)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
