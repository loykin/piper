package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/pipeline/run"
)

type runRepo struct{ sqlxadapter.Source }

func NewRunRepo(exec *dbstore.Executor[*sqlx.DB], source string) run.Repository {
	return &runRepo{Source: sqlxadapter.NewSource(source, exec)}
}

const runSelectCols = `project_id, id, schedule_id, experiment, pipeline_name, status, started_at, ended_at, scheduled_at, pipeline_yaml, params_json, created_by, storage_backend`

func (r *runRepo) Create(ctx context.Context, row *run.Run) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.NamedExecContext(ctx,
			`INSERT INTO runs (project_id, id, schedule_id, experiment, pipeline_name, status, started_at, scheduled_at, pipeline_yaml, params_json, created_by, storage_backend)
			 VALUES (:project_id, :id, :schedule_id, :experiment, :pipeline_name, :status, :started_at, :scheduled_at, :pipeline_yaml, :params_json, :created_by, :storage_backend)`,
			row)
		return err
	})
}

func (r *runRepo) Get(ctx context.Context, projectID, id string) (*run.Run, error) {
	var v run.Run
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &v,
			`SELECT `+runSelectCols+` FROM runs WHERE project_id=? AND id=?`, projectID, id)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *runRepo) List(ctx context.Context, projectID string, filter run.RunFilter) ([]*run.Run, error) {
	metricSort := filter.MetricStep != "" && filter.MetricKey != ""
	var query string
	var args []any
	var where []string

	if metricSort {
		query = `SELECT r.project_id, r.id, r.schedule_id, r.experiment, r.pipeline_name, r.status, r.started_at, r.ended_at, r.scheduled_at, r.pipeline_yaml, r.params_json, r.created_by, r.storage_backend
FROM runs r
LEFT JOIN (SELECT project_id, run_id, MAX(value) AS mv FROM run_metrics WHERE project_id=? AND step_name=? AND key=? GROUP BY project_id, run_id) m
	ON m.project_id=r.project_id AND m.run_id=r.id`
		args = append(args, projectID, filter.MetricStep, filter.MetricKey)
		where = append(where, "r.project_id=?")
		args = append(args, projectID)
		if filter.Experiment != "" {
			where = append(where, "r.experiment=?")
			args = append(args, filter.Experiment)
		}
		if filter.PipelineName != "" {
			where = append(where, "r.pipeline_name=?")
			args = append(args, filter.PipelineName)
		}
		if filter.ScheduleID != "" {
			where = append(where, "r.schedule_id=?")
			args = append(args, filter.ScheduleID)
		}
		if filter.Status != "" {
			where = append(where, "r.status=?")
			args = append(args, filter.Status)
		}
	} else {
		query = `SELECT ` + runSelectCols + ` FROM runs`
		where = append(where, "project_id=?")
		args = append(args, projectID)
		if filter.Experiment != "" {
			where = append(where, "experiment=?")
			args = append(args, filter.Experiment)
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
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if metricSort {
		order := "DESC"
		if filter.MetricOrder == "asc" {
			order = "ASC"
		}
		// id as a tiebreaker: two runs can tie on the sorted metric value (or
		// both lack one), and without a unique secondary key offset paging
		// isn't guaranteed a stable order — the same row can appear on two
		// pages, or get skipped, if ties land differently across queries.
		query += " ORDER BY m.mv " + order + " NULLS LAST, r.id " + order
	} else {
		query += " ORDER BY started_at DESC, id DESC"
	}
	if filter.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, filter.Limit, filter.Offset)
	}

	var out []*run.Run
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, query, args...)
	})
	if out == nil {
		out = []*run.Run{}
	}
	return out, err
}

func (r *runRepo) ListTerminalBefore(ctx context.Context, projectID string, cutoff time.Time) ([]*run.Run, error) {
	var out []*run.Run
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT `+runSelectCols+` FROM runs
			 WHERE project_id=? AND ended_at IS NOT NULL AND ended_at<?
			   AND status NOT IN ('running', 'scheduled')
			 ORDER BY ended_at ASC`,
			projectID, cutoff)
	})
	if out == nil {
		out = []*run.Run{}
	}
	return out, err
}

func (r *runRepo) ExistingIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var found []string
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		query, args, err := sqlx.In(`SELECT DISTINCT id FROM runs WHERE id IN (?)`, ids)
		if err != nil {
			return err
		}
		return db.SelectContext(ctx, &found, db.Rebind(query), args...)
	})
	if err != nil {
		return nil, err
	}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

func (r *runRepo) Count(ctx context.Context, projectID string, filter run.RunFilter) (int, error) {
	query := `SELECT COUNT(*) FROM runs`
	where := []string{"project_id=?"}
	args := []any{projectID}
	if filter.Experiment != "" {
		where = append(where, "experiment=?")
		args = append(args, filter.Experiment)
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
	query += " WHERE " + strings.Join(where, " AND ")

	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &count, query, args...)
	})
	return count, err
}

func (r *runRepo) UpdateStatus(ctx context.Context, projectID, id, status string, endedAt *time.Time) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE runs SET status=?, ended_at=? WHERE project_id=? AND id=?`, status, endedAt, projectID, id)
		return err
	})
}

func (r *runRepo) FinalizeStatusCAS(ctx context.Context, projectID, id, to string, endedAt *time.Time) (bool, error) {
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx,
			`UPDATE runs SET status=?, ended_at=? WHERE project_id=? AND id=? AND status NOT IN ('success', 'failed', 'canceled')`,
			to, endedAt, projectID, id)
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

func (r *runRepo) MarkRunning(ctx context.Context, projectID, id string, startedAt time.Time) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE runs SET status='running', started_at=? WHERE project_id=? AND id=?`, startedAt, projectID, id)
		return err
	})
}

func (r *runRepo) Delete(ctx context.Context, projectID, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM runs WHERE project_id=? AND id=?`, projectID, id)
		return err
	})
}

func (r *runRepo) GetLatestSuccessful(ctx context.Context, projectID, pipelineName string) (*run.Run, error) {
	var v run.Run
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &v,
			`SELECT `+runSelectCols+` FROM runs WHERE project_id=? AND pipeline_name=? AND status='success' ORDER BY started_at DESC LIMIT 1`,
			projectID, pipelineName)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
