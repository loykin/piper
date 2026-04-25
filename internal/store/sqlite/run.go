package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/run"
)

type runRepo struct{ db *sqlx.DB }

func NewRunRepo(db *sqlx.DB) run.Repository { return &runRepo{db: db} }

func (r *runRepo) Create(ctx context.Context, row *run.Run) error {
	_, err := r.db.NamedExecContext(ctx,
		`INSERT INTO runs (id, schedule_id, owner_id, pipeline_name, status, started_at, scheduled_at, pipeline_yaml)
		 VALUES (:id, :schedule_id, :owner_id, :pipeline_name, :status, :started_at, :scheduled_at, :pipeline_yaml)`,
		row)
	return err
}

func (r *runRepo) Get(ctx context.Context, id string) (*run.Run, error) {
	var v run.Run
	err := r.db.GetContext(ctx, &v,
		`SELECT id, schedule_id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at FROM runs WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	return &v, nil
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
	var out []*run.Run
	err := r.db.SelectContext(ctx, &out, query, args...)
	if out == nil {
		out = []*run.Run{}
	}
	return out, err
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
	var v run.Run
	err := r.db.GetContext(ctx, &v,
		`SELECT id, schedule_id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at
		 FROM runs WHERE pipeline_name=? AND status='success' ORDER BY started_at DESC LIMIT 1`,
		pipelineName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
