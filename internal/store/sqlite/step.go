package sqlite

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/pipeline/run"
)

type stepRepo struct{ dbstore.BaseRepo }

func NewStepRepo(exec *dbstore.Executor, source string) run.StepRepository {
	return &stepRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *stepRepo) Upsert(ctx context.Context, s *run.Step) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.NamedExecContext(ctx, `
			INSERT INTO steps (project_id, run_id, step_name, status, started_at, ended_at, error, attempts)
			VALUES (:project_id, :run_id, :step_name, :status, :started_at, :ended_at, :error, :attempts)
			ON CONFLICT(project_id, run_id, step_name) DO UPDATE SET
				status=excluded.status, started_at=excluded.started_at,
				ended_at=excluded.ended_at, error=excluded.error, attempts=excluded.attempts
		`, s)
		return err
	})
}

func (r *stepRepo) List(ctx context.Context, projectID, runID string) ([]*run.Step, error) {
	var steps []*run.Step
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &steps,
			`SELECT project_id, run_id, step_name, status, started_at, ended_at, error, attempts
			 FROM steps WHERE project_id=? AND run_id=?`, projectID, runID)
	})
	if steps == nil {
		steps = []*run.Step{}
	}
	return steps, err
}

func (r *stepRepo) ListByRuns(ctx context.Context, projectID string, runIDs []string) (map[string][]*run.Step, error) {
	out := make(map[string][]*run.Step, len(runIDs))
	if len(runIDs) == 0 {
		return out, nil
	}
	var steps []*run.Step
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		query, args, err := sqlx.In(
			`SELECT project_id, run_id, step_name, status, started_at, ended_at, error, attempts
			 FROM steps WHERE project_id=? AND run_id IN (?)`,
			projectID, runIDs,
		)
		if err != nil {
			return err
		}
		query = db.Rebind(query)
		return db.SelectContext(ctx, &steps, query, args...)
	})
	if err != nil {
		return nil, err
	}
	for _, step := range steps {
		out[step.RunID] = append(out[step.RunID], step)
	}
	return out, nil
}

func (r *stepRepo) DeleteByRun(ctx context.Context, projectID, runID string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM steps WHERE project_id=? AND run_id=?`, projectID, runID)
		return err
	})
}
