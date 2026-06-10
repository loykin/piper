package logstore

import (
	"database/sql"

	"github.com/piper/piper/internal/redact"
)

// SQLiteLogStore implements LogStore using the existing logs table.
type SQLiteLogStore struct {
	db *sql.DB
}

func (s *SQLiteLogStore) AppendMetrics(metrics []*Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO run_metrics (run_id, step_name, key, value, recorded_at) VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, m := range metrics {
		if _, err := stmt.Exec(m.RunID, m.StepName, redact.String(m.Key), m.Value, m.Ts); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteLogStore) QueryMetrics(runID, stepName string) ([]*Metric, error) {
	query := `SELECT id, run_id, step_name, key, value, recorded_at FROM run_metrics WHERE run_id=?`
	args := []any{runID}
	if stepName != "" {
		query += ` AND step_name=?`
		args = append(args, stepName)
	}
	query += ` ORDER BY recorded_at ASC, id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Metric
	for rows.Next() {
		var m Metric
		if err := rows.Scan(&m.ID, &m.RunID, &m.StepName, &m.Key, &m.Value, &m.Ts); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// NewSQLite wraps an existing *sql.DB as a LogStore.
// The logs table must already exist (managed by store.migrate).
func NewSQLite(db *sql.DB) *SQLiteLogStore {
	return &SQLiteLogStore{db: db}
}

func (s *SQLiteLogStore) Append(lines []*Line) error {
	if len(lines) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO logs (run_id, step_name, ts, stream, line) VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, l := range lines {
		l.Line = redact.String(l.Line)
		if _, err := stmt.Exec(l.RunID, l.StepName, l.Ts, l.Stream, l.Line); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteLogStore) Query(runID, stepName string, afterID int64) ([]*Line, error) {
	rows, err := s.db.Query(
		`SELECT id, run_id, step_name, ts, stream, line
		 FROM logs WHERE run_id=? AND step_name=? AND id>?
		 ORDER BY id ASC`,
		runID, stepName, afterID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Line
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.RunID, &l.StepName, &l.Ts, &l.Stream, &l.Line); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}
