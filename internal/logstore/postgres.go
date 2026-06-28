package logstore

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/internal/redact"
)

// PgStore implements LogStore and MetricStore using PostgreSQL via dbstore.Executor.
type PgStore struct {
	exec   *dbstore.Executor
	source string
}

// NewPostgres creates a PgStore that routes all DB access through the executor.
func NewPostgres(exec *dbstore.Executor, source string) *PgStore {
	return &PgStore{exec: exec, source: source}
}

func (s *PgStore) Append(ctx context.Context, lines []*Line) error {
	if len(lines) == 0 {
		return nil
	}
	return s.exec.RunTx(ctx, s.source, func(ctx context.Context, tx *sqlx.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO logs (project_id, run_id, step_name, ts, stream, line) VALUES ($1, $2, $3, $4, $5, $6)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, l := range lines {
			line := redact.String(l.Line)
			if _, err := stmt.ExecContext(ctx, l.ProjectID, l.RunID, l.StepName, l.Ts, l.Stream, line); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *PgStore) Query(projectID, runID, stepName string, afterID int64) ([]*Line, error) {
	var out []*Line
	err := s.exec.Run(context.Background(), s.source, func(ctx context.Context, db *sqlx.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT id, project_id, run_id, step_name, ts, stream, line
			 FROM logs WHERE project_id=$1 AND run_id=$2 AND step_name=$3 AND id>$4
			 ORDER BY id ASC`,
			projectID, runID, stepName, afterID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var l Line
			if err := rows.Scan(&l.ID, &l.ProjectID, &l.RunID, &l.StepName, &l.Ts, &l.Stream, &l.Line); err != nil {
				return err
			}
			out = append(out, &l)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgStore) AppendMetrics(ctx context.Context, metrics []*Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	return s.exec.RunTx(ctx, s.source, func(ctx context.Context, tx *sqlx.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO run_metrics (project_id, run_id, step_name, key, value, recorded_at) VALUES ($1, $2, $3, $4, $5, $6)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, m := range metrics {
			key := redact.String(m.Key)
			if _, err := stmt.ExecContext(ctx, m.ProjectID, m.RunID, m.StepName, key, m.Value, m.Ts); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *PgStore) QueryMetrics(projectID, runID, stepName string) ([]*Metric, error) {
	query := `SELECT id, project_id, run_id, step_name, key, value, recorded_at FROM run_metrics WHERE project_id=$1 AND run_id=$2`
	args := []any{projectID, runID}
	if stepName != "" {
		query += ` AND step_name=$3`
		args = append(args, stepName)
	}
	query += ` ORDER BY recorded_at ASC, id ASC`

	var out []*Metric
	err := s.exec.Run(context.Background(), s.source, func(ctx context.Context, db *sqlx.DB) error {
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var m Metric
			if err := rows.Scan(&m.ID, &m.ProjectID, &m.RunID, &m.StepName, &m.Key, &m.Value, &m.Ts); err != nil {
				return err
			}
			out = append(out, &m)
		}
		return rows.Err()
	})
	return out, err
}
