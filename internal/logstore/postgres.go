package logstore

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/internal/redact"
	"github.com/loykin/piper/pkg/statsstore"
)

// PgStore implements LogStore and MetricStore using PostgreSQL via dbstore.Executor.
type PgStore struct {
	exec   *dbstore.Executor[*sqlx.DB]
	source string
}

// NewPostgres creates a PgStore that routes all DB access through the executor.
func NewPostgres(exec *dbstore.Executor[*sqlx.DB], source string) *PgStore {
	return &PgStore{exec: exec, source: source}
}

func (s *PgStore) Append(ctx context.Context, lines []*Line) error {
	if len(lines) == 0 {
		return nil
	}
	return sqlxadapter.RunTx(s.exec, ctx, s.source, func(ctx context.Context, tx *sqlx.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO logs (event_id, project_id, run_id, step_name, ts, stream, line)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (event_id) WHERE event_id <> '' DO NOTHING`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, l := range lines {
			if l.EventID == "" {
				l.EventID = uuid.NewString()
			}
			line := redact.String(l.Line)
			if _, err := stmt.ExecContext(ctx, l.EventID, l.ProjectID, l.RunID, l.StepName, l.Ts, l.Stream, line); err != nil {
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
			`SELECT id, event_id, project_id, run_id, step_name, ts, stream, line
			 FROM logs WHERE project_id=$1 AND run_id=$2 AND step_name=$3 AND id>$4
			 ORDER BY id ASC`,
			projectID, runID, stepName, afterID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var l Line
			if err := rows.Scan(&l.ID, &l.EventID, &l.ProjectID, &l.RunID, &l.StepName, &l.Ts, &l.Stream, &l.Line); err != nil {
				return err
			}
			out = append(out, &l)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgStore) QueryLogPage(ctx context.Context, query statsstore.LogQuery) (statsstore.LogPage, error) {
	return queryRelationalLogPage(ctx, s.exec, s.source, query)
}

func (s *PgStore) QueryMetricPage(ctx context.Context, query statsstore.MetricQuery) (statsstore.MetricPage, error) {
	return queryRelationalMetricPage(ctx, s.exec, s.source, query)
}

func (s *PgStore) PurgeProject(ctx context.Context, projectID string) error {
	return purgeRelationalStats(ctx, s.exec, s.source, projectID, "")
}

func (s *PgStore) PurgeRun(ctx context.Context, projectID, runID string) error {
	return purgeRelationalStats(ctx, s.exec, s.source, projectID, runID)
}

func (s *PgStore) AppendMetrics(ctx context.Context, metrics []*Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	return sqlxadapter.RunTx(s.exec, ctx, s.source, func(ctx context.Context, tx *sqlx.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO run_metrics (event_id, project_id, run_id, step_name, key, value, recorded_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (event_id) WHERE event_id <> '' DO NOTHING`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, m := range metrics {
			if m.EventID == "" {
				m.EventID = uuid.NewString()
			}
			key := redact.String(m.Key)
			if _, err := stmt.ExecContext(ctx, m.EventID, m.ProjectID, m.RunID, m.StepName, key, m.Value, m.Ts); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *PgStore) QueryMetrics(projectID, runID, stepName string) ([]*Metric, error) {
	query := `SELECT id, event_id, project_id, run_id, step_name, key, value, recorded_at FROM run_metrics WHERE project_id=$1 AND run_id=$2`
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
			if err := rows.Scan(&m.ID, &m.EventID, &m.ProjectID, &m.RunID, &m.StepName, &m.Key, &m.Value, &m.Ts); err != nil {
				return err
			}
			out = append(out, &m)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgStore) SweepLogs(ctx context.Context, before time.Time, limit int) (int64, error) {
	return s.sweep(ctx, `DELETE FROM logs WHERE id IN (SELECT id FROM logs WHERE ts < $1 ORDER BY ts, id LIMIT $2)`, before, limit)
}

func (s *PgStore) SweepMetrics(ctx context.Context, before time.Time, limit int) (int64, error) {
	return s.sweep(ctx, `DELETE FROM run_metrics WHERE id IN (SELECT id FROM run_metrics WHERE recorded_at < $1 ORDER BY recorded_at, id LIMIT $2)`, before, limit)
}

func (s *PgStore) sweep(ctx context.Context, query string, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	var deleted int64
	err := s.exec.Run(ctx, s.source, func(ctx context.Context, db *sqlx.DB) error {
		result, err := db.ExecContext(ctx, query, before.UTC(), limit)
		if err != nil {
			return err
		}
		deleted, err = result.RowsAffected()
		return err
	})
	return deleted, err
}
