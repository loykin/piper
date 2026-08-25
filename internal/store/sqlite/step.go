package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/pipeline/run"
)

type stepRepo struct{ sqlxadapter.Source }

func NewStepRepo(exec *dbstore.Executor[*sqlx.DB], source string) run.StepRepository {
	return &stepRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *stepRepo) Upsert(ctx context.Context, s *run.Step) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		normalized := *s
		normalized.StartedAt = utcPtr(s.StartedAt)
		normalized.EndedAt = utcPtr(s.EndedAt)
		_, err := db.NamedExecContext(ctx, `
			INSERT INTO steps (project_id, run_id, step_name, status, started_at, ended_at, error, attempts)
			VALUES (:project_id, :run_id, :step_name, :status, :started_at, :ended_at, :error, :attempts)
			ON CONFLICT(project_id, run_id, step_name) DO UPDATE SET
				status=excluded.status, started_at=excluded.started_at,
				ended_at=excluded.ended_at, error=excluded.error, attempts=excluded.attempts
		`, &normalized)
		return err
	})
}

func (r *stepRepo) UpsertCAS(ctx context.Context, s *run.Step) (bool, error) {
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		normalized := *s
		normalized.StartedAt = utcPtr(s.StartedAt)
		normalized.EndedAt = utcPtr(s.EndedAt)
		res, err := db.NamedExecContext(ctx, `
			INSERT INTO steps (project_id, run_id, step_name, status, started_at, ended_at, error, attempts)
			VALUES (:project_id, :run_id, :step_name, :status, :started_at, :ended_at, :error, :attempts)
			ON CONFLICT(project_id, run_id, step_name) DO UPDATE SET
				status=excluded.status, started_at=excluded.started_at,
				ended_at=excluded.ended_at, error=excluded.error, attempts=excluded.attempts
			WHERE excluded.attempts >= steps.attempts
		`, &normalized)
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

// utcPtr normalizes t to UTC. Timestamps that cross a process/network
// boundary (e.g. a worker's JSON-encoded task result) can carry a numeric,
// unnamed timezone offset; SQLite's default text-based time round-trip
// cannot parse that back into a time.Time on read ("unsupported Scan ...
// into type *time.Time"). A UTC location always round-trips.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
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
