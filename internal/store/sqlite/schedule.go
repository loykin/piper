package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/schedule"
)

type scheduleRepo struct{ dbstore.BaseRepo }

func NewScheduleRepo(exec *dbstore.Executor, source string) schedule.Repository {
	return &scheduleRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

// scheduleRow is the DB scan target; maps int-stored enabled to bool.
type scheduleRow struct {
	ProjectID    string     `db:"project_id"`
	ID           string     `db:"id"`
	Name         string     `db:"name"`
	PipelineYAML string     `db:"pipeline_yaml"`
	VersionID    string     `db:"template_version_id"`
	ScheduleType string     `db:"schedule_type"`
	CronExpr     string     `db:"cron_expr"`
	EnabledInt   int        `db:"enabled"`
	MaxRuns      int        `db:"max_runs"`
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
		VersionID:    s.VersionID,
		ScheduleType: s.ScheduleType,
		CronExpr:     s.CronExpr,
		Enabled:      s.EnabledInt == 1,
		MaxRuns:      s.MaxRuns,
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

const scheduleSelectCols = `project_id, id, name, pipeline_yaml, template_version_id, cron_expr, params_json, enabled, max_runs, last_run_at, next_run_at, created_at, updated_at, schedule_type`

func (r *scheduleRepo) Create(ctx context.Context, sc *schedule.Schedule) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO schedules (project_id, id, name, pipeline_yaml, template_version_id, cron_expr, params_json, enabled, max_runs, last_run_at, next_run_at, created_at, updated_at, schedule_type)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sc.ProjectID, sc.ID, sc.Name, sc.PipelineYAML, sc.VersionID, sc.CronExpr, sc.ParamsJSON,
			boolToInt(sc.Enabled), sc.MaxRuns, sc.LastRunAt, sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt, sc.ScheduleType,
		)
		return err
	})
}

func (r *scheduleRepo) Get(ctx context.Context, projectID, id string) (*schedule.Schedule, error) {
	var row scheduleRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &row,
			`SELECT `+scheduleSelectCols+` FROM schedules WHERE project_id=? AND id=?`, projectID, id)
	})
	if err != nil {
		return nil, err
	}
	return row.toSchedule(), nil
}

func (r *scheduleRepo) List(ctx context.Context, projectID string) ([]*schedule.Schedule, error) {
	var rows []scheduleRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows,
			`SELECT `+scheduleSelectCols+` FROM schedules WHERE project_id=? ORDER BY created_at DESC`, projectID)
	})
	if err != nil {
		return nil, err
	}
	out := make([]*schedule.Schedule, len(rows))
	for i, row := range rows {
		out[i] = row.toSchedule()
	}
	return out, nil
}

func (r *scheduleRepo) ListWithMaxRuns(ctx context.Context) ([]*schedule.Schedule, error) {
	var rows []scheduleRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows,
			`SELECT `+scheduleSelectCols+` FROM schedules WHERE max_runs > 0 ORDER BY project_id ASC, created_at ASC`)
	})
	if err != nil {
		return nil, err
	}
	out := make([]*schedule.Schedule, len(rows))
	for i, row := range rows {
		out[i] = row.toSchedule()
	}
	return out, nil
}

func (r *scheduleRepo) ListEnabled(ctx context.Context) ([]*schedule.Schedule, error) {
	var rows []scheduleRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows,
			`SELECT `+scheduleSelectCols+` FROM schedules WHERE enabled=1 ORDER BY created_at ASC`)
	})
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
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows,
			`SELECT `+scheduleSelectCols+` FROM schedules WHERE enabled=1 AND next_run_at <= ? ORDER BY next_run_at ASC`, now)
	})
	if err != nil {
		return nil, err
	}
	out := make([]*schedule.Schedule, len(rows))
	for i, row := range rows {
		out[i] = row.toSchedule()
	}
	return out, nil
}

func (r *scheduleRepo) ClaimRun(ctx context.Context, projectID, id string, expectedAt, lastRunAt, nextRunAt time.Time) (bool, error) {
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx,
			`UPDATE schedules SET last_run_at=?, next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=1 AND next_run_at=?`,
			lastRunAt, nextRunAt, time.Now().UTC(), projectID, id, expectedAt)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (r *scheduleRepo) AdvanceNextRun(ctx context.Context, projectID, id string, expectedAt, nextRunAt time.Time) (bool, error) {
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx,
			`UPDATE schedules SET next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=1 AND next_run_at=?`,
			nextRunAt, time.Now().UTC(), projectID, id, expectedAt)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (r *scheduleRepo) ClaimOneShotRun(ctx context.Context, projectID, id string, expectedAt, lastRunAt time.Time) (bool, error) {
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx,
			`UPDATE schedules SET enabled=0, last_run_at=?, next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=1 AND next_run_at=?`,
			lastRunAt, lastRunAt, time.Now().UTC(), projectID, id, expectedAt)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (r *scheduleRepo) SetEnabled(ctx context.Context, projectID, id string, enabled bool) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE schedules SET enabled=?, updated_at=? WHERE project_id=? AND id=?`,
			boolToInt(enabled), time.Now().UTC(), projectID, id)
		return err
	})
}

func (r *scheduleRepo) SetMaxRuns(ctx context.Context, projectID, id string, maxRuns int) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE schedules SET max_runs=?, updated_at=? WHERE project_id=? AND id=?`,
			maxRuns, time.Now().UTC(), projectID, id)
		return err
	})
}

func (r *scheduleRepo) Delete(ctx context.Context, projectID, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM schedules WHERE project_id=? AND id=?`, projectID, id)
		return err
	})
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
