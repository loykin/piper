package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/pipeline/run"
)

type runRepo struct{ db *sqlx.DB }

func NewRunRepo(db *sqlx.DB) run.Repository { return &runRepo{db: db} }

const pgRunSelectCols = `project_id, id, schedule_id, experiment, pipeline_name, status, started_at, ended_at, scheduled_at, pipeline_yaml, params_json`

func (r *runRepo) Create(ctx context.Context, row *run.Run) error {
	_, err := r.db.NamedExecContext(ctx,
		`INSERT INTO runs (project_id, id, schedule_id, experiment, pipeline_name, status, started_at, scheduled_at, pipeline_yaml, params_json)
		 VALUES (:project_id, :id, :schedule_id, :experiment, :pipeline_name, :status, :started_at, :scheduled_at, :pipeline_yaml, :params_json)`,
		row)
	return err
}

func (r *runRepo) Get(ctx context.Context, projectID, id string) (*run.Run, error) {
	var v run.Run
	q := r.db.Rebind(`SELECT ` + pgRunSelectCols + ` FROM runs WHERE project_id=? AND id=?`)
	err := r.db.GetContext(ctx, &v, q, projectID, id)
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
		query = `SELECT r.project_id, r.id, r.schedule_id, r.experiment, r.pipeline_name, r.status, r.started_at, r.ended_at, r.scheduled_at, r.pipeline_yaml, r.params_json
FROM runs r
LEFT JOIN (SELECT project_id, run_id, MAX(value) AS mv FROM run_metrics WHERE project_id=? AND step_name=? AND key=? GROUP BY project_id, run_id) m
	ON m.project_id=r.project_id AND m.run_id=r.id`
		args = append(args, projectID)
		args = append(args, filter.MetricStep, filter.MetricKey)
		where = append(where, "r.project_id=?")
		args = append(args, projectID)
		where = append(where, "r.experiment=?")
		args = append(args, filter.Experiment)
	} else {
		query = `SELECT ` + pgRunSelectCols + ` FROM runs`
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
		query += " ORDER BY m.mv " + order + " NULLS LAST"
	} else {
		query += " ORDER BY started_at DESC"
	}

	query = r.db.Rebind(query)
	var out []*run.Run
	err := r.db.SelectContext(ctx, &out, query, args...)
	if out == nil {
		out = []*run.Run{}
	}
	return out, err
}

func (r *runRepo) UpdateStatus(ctx context.Context, projectID, id, status string, endedAt *time.Time) error {
	q := r.db.Rebind(`UPDATE runs SET status=?, ended_at=? WHERE project_id=? AND id=?`)
	_, err := r.db.ExecContext(ctx, q, status, endedAt, projectID, id)
	return err
}

func (r *runRepo) MarkRunning(ctx context.Context, projectID, id string, startedAt time.Time) error {
	q := r.db.Rebind(`UPDATE runs SET status='running', started_at=? WHERE project_id=? AND id=?`)
	_, err := r.db.ExecContext(ctx, q, startedAt, projectID, id)
	return err
}

func (r *runRepo) Delete(ctx context.Context, projectID, id string) error {
	q := r.db.Rebind(`DELETE FROM runs WHERE project_id=? AND id=?`)
	_, err := r.db.ExecContext(ctx, q, projectID, id)
	return err
}

func (r *runRepo) GetLatestSuccessful(ctx context.Context, projectID, pipelineName string) (*run.Run, error) {
	var v run.Run
	q := r.db.Rebind(`SELECT ` + pgRunSelectCols + `
		 FROM runs WHERE project_id=? AND pipeline_name=? AND status='success' ORDER BY started_at DESC LIMIT 1`)
	err := r.db.GetContext(ctx, &v, q, projectID, pipelineName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
