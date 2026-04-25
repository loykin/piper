package sqlite

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/run"
)

type stepRepo struct{ db *sqlx.DB }

func NewStepRepo(db *sqlx.DB) run.StepRepository { return &stepRepo{db: db} }

func (r *stepRepo) Upsert(ctx context.Context, s *run.Step) error {
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO steps (run_id, step_name, status, started_at, ended_at, error, attempts)
		VALUES (:run_id, :step_name, :status, :started_at, :ended_at, :error, :attempts)
		ON CONFLICT(run_id, step_name) DO UPDATE SET
			status=excluded.status, started_at=excluded.started_at,
			ended_at=excluded.ended_at, error=excluded.error, attempts=excluded.attempts
	`, s)
	return err
}

func (r *stepRepo) List(ctx context.Context, runID string) ([]*run.Step, error) {
	var steps []*run.Step
	err := r.db.SelectContext(ctx, &steps,
		`SELECT run_id, step_name, status, started_at, ended_at, error, attempts FROM steps WHERE run_id=?`, runID)
	if steps == nil {
		steps = []*run.Step{}
	}
	return steps, err
}

func (r *stepRepo) DeleteByRun(ctx context.Context, runID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM steps WHERE run_id=?`, runID)
	return err
}
