package sqlite

import (
	"context"
	"database/sql"

	"github.com/piper/piper/pkg/run"
)

type stepRepo struct{ db *sql.DB }

func NewStepRepo(db *sql.DB) run.StepRepository { return &stepRepo{db: db} }

func (r *stepRepo) Upsert(ctx context.Context, s *run.Step) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO steps (run_id, step_name, status, started_at, ended_at, error, attempts)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, step_name) DO UPDATE SET
			status=excluded.status, started_at=excluded.started_at,
			ended_at=excluded.ended_at, error=excluded.error, attempts=excluded.attempts
	`, s.RunID, s.StepName, s.Status, s.StartedAt, s.EndedAt, s.Error, s.Attempts)
	return err
}

func (r *stepRepo) List(ctx context.Context, runID string) ([]*run.Step, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT run_id, step_name, status, started_at, ended_at, error, attempts FROM steps WHERE run_id=?`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var steps []*run.Step
	for rows.Next() {
		var s run.Step
		var startedAt, endedAt sql.NullTime
		var errMsg sql.NullString
		if err := rows.Scan(&s.RunID, &s.StepName, &s.Status, &startedAt, &endedAt, &errMsg, &s.Attempts); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			s.StartedAt = &startedAt.Time
		}
		if endedAt.Valid {
			s.EndedAt = &endedAt.Time
		}
		if errMsg.Valid {
			s.Error = errMsg.String
		}
		steps = append(steps, &s)
	}
	return steps, rows.Err()
}

func (r *stepRepo) DeleteByRun(ctx context.Context, runID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM steps WHERE run_id=?`, runID)
	return err
}
