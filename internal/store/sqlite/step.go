package sqlite

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
			status=excluded.status, started_at=excluded.started_at,
			ended_at=excluded.ended_at, error=excluded.error, attempts=excluded.attempts
	`, s)
	return err
}

func (r *stepRepo) List(ctx context.Context, projectID, runID string) ([]*run.Step, error) {
	var steps []*run.Step
	err := r.db.SelectContext(ctx, &steps,
		`SELECT project_id, run_id, step_name, status, started_at, ended_at, error, attempts
		 FROM steps WHERE project_id=? AND run_id=?`, projectID, runID)
	if steps == nil {
		steps = []*run.Step{}
	}
	return steps, err
}

func (r *stepRepo) DeleteByRun(ctx context.Context, projectID, runID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM steps WHERE project_id=? AND run_id=?`, projectID, runID)
	return err
}
