package postgres

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

const scheduleSelectCols = `project_id, id, name, pipeline_yaml, template_version_id, cron_expr, params_json, enabled, max_runs, last_run_at, next_run_at, created_at, updated_at, schedule_type`

func (r *scheduleRepo) Create(ctx context.Context, sc *schedule.Schedule) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO schedules (project_id, id, name, pipeline_yaml, template_version_id, cron_expr, params_json, enabled, max_runs, last_run_at, next_run_at, created_at, updated_at, schedule_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q,
			sc.ProjectID, sc.ID, sc.Name, sc.PipelineYAML, sc.VersionID, sc.CronExpr, sc.ParamsJSON,
			sc.Enabled, sc.MaxRuns, sc.LastRunAt, sc.NextRunAt, sc.CreatedAt, sc.UpdatedAt, sc.ScheduleType,
		)
		return err
	})
}

func (r *scheduleRepo) Get(ctx context.Context, projectID, id string) (*schedule.Schedule, error) {
	var sc schedule.Schedule
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE project_id=? AND id=?`)
		return db.GetContext(ctx, &sc, q, projectID, id)
	})
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
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE project_id=? ORDER BY created_at DESC`)
		return db.SelectContext(ctx, &rows, q, projectID)
	})
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

func (r *scheduleRepo) ListWithMaxRuns(ctx context.Context) ([]*schedule.Schedule, error) {
	var rows []*schedule.Schedule
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE max_runs > ? ORDER BY project_id ASC, created_at ASC`)
		return db.SelectContext(ctx, &rows, q, 0)
	})
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
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE enabled=? ORDER BY created_at ASC`)
		return db.SelectContext(ctx, &rows, q, true)
	})
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
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + scheduleSelectCols + ` FROM schedules WHERE enabled=? AND next_run_at <= ? ORDER BY next_run_at ASC`)
		return db.SelectContext(ctx, &rows, q, true, now)
	})
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
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE schedules SET last_run_at=?, next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=? AND next_run_at=?`)
		res, err := db.ExecContext(ctx, q, lastRunAt, nextRunAt, time.Now().UTC(), projectID, id, true, expectedAt)
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
		q := db.Rebind(`UPDATE schedules SET next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=? AND next_run_at=?`)
		res, err := db.ExecContext(ctx, q, nextRunAt, time.Now().UTC(), projectID, id, true, expectedAt)
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
		q := db.Rebind(`UPDATE schedules SET enabled=?, last_run_at=?, next_run_at=?, updated_at=? WHERE project_id=? AND id=? AND enabled=? AND next_run_at=?`)
		res, err := db.ExecContext(ctx, q, false, lastRunAt, lastRunAt, time.Now().UTC(), projectID, id, true, expectedAt)
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
		q := db.Rebind(`UPDATE schedules SET enabled=?, updated_at=? WHERE project_id=? AND id=?`)
		_, err := db.ExecContext(ctx, q, enabled, time.Now().UTC(), projectID, id)
		return err
	})
}

func (r *scheduleRepo) SetMaxRuns(ctx context.Context, projectID, id string, maxRuns int) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE schedules SET max_runs=?, updated_at=? WHERE project_id=? AND id=?`)
		_, err := db.ExecContext(ctx, q, maxRuns, time.Now().UTC(), projectID, id)
		return err
	})
}

func (r *scheduleRepo) Delete(ctx context.Context, projectID, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM schedules WHERE project_id=? AND id=?`)
		_, err := db.ExecContext(ctx, q, projectID, id)
		return err
	})
}
