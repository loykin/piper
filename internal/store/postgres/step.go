package postgres

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/pipeline/run"
)

type stepRepo struct{ db *sqlx.DB }

func NewStepRepo(db *sqlx.DB) run.StepRepository { return &stepRepo{db: db} }

func (r *stepRepo) Upsert(ctx context.Context, s *run.Step) error {
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO steps (project_id, run_id, step_name, status, started_at, ended_at, error, attempts)
		VALUES (:project_id, :run_id, :step_name, :status, :started_at, :ended_at, :error, :attempts)
		ON CONFLICT(project_id, run_id, step_name) DO UPDATE SET
			status=EXCLUDED.status, started_at=EXCLUDED.started_at,
			ended_at=EXCLUDED.ended_at, error=EXCLUDED.error, attempts=EXCLUDED.attempts
	`, s)
	return err
}

func (r *stepRepo) List(ctx context.Context, projectID, runID string) ([]*run.Step, error) {
	var steps []*run.Step
	q := r.db.Rebind(`SELECT project_id, run_id, step_name, status, started_at, ended_at, error, attempts
		FROM steps WHERE project_id=? AND run_id=?`)
	err := r.db.SelectContext(ctx, &steps, q, projectID, runID)
	if steps == nil {
		steps = []*run.Step{}
	}
	return steps, err
}

func (r *stepRepo) DeleteByRun(ctx context.Context, projectID, runID string) error {
	q := r.db.Rebind(`DELETE FROM steps WHERE project_id=? AND run_id=?`)
	_, err := r.db.ExecContext(ctx, q, projectID, runID)
	return err
}
