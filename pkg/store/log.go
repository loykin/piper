package store

import "time"

type LogLine struct {
	ID       int64     `json:"id"`
	RunID    string    `json:"run_id"`
	StepName string    `json:"step_name"`
	Ts       time.Time `json:"ts"`
	Stream   string    `json:"stream"` // stdout | stderr
	Line     string    `json:"line"`
}

func (s *Store) AppendLog(l *LogLine) error {
	_, err := s.db.Exec(
		`INSERT INTO logs (run_id, step_name, ts, stream, line) VALUES (?, ?, ?, ?, ?)`,
		l.RunID, l.StepName, l.Ts, l.Stream, l.Line,
	)
	return err
}

// AppendLogs persists a batch of log lines.
func (s *Store) AppendLogs(lines []*LogLine) error {
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
		if _, err := stmt.Exec(l.RunID, l.StepName, l.Ts, l.Stream, l.Line); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// GetLogs retrieves logs for a step. If afterID > 0, only logs after that ID are returned (for real-time polling).
func (s *Store) GetLogs(runID, stepName string, afterID int64) ([]*LogLine, error) {
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

	var lines []*LogLine
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.ID, &l.RunID, &l.StepName, &l.Ts, &l.Stream, &l.Line); err != nil {
			return nil, err
		}
		lines = append(lines, &l)
	}
	return lines, rows.Err()
}
