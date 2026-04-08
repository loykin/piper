package store

import (
	"database/sql"
	"time"
)

type Step struct {
	RunID     string     `json:"run_id"`
	StepName  string     `json:"step_name"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	Attempts  int        `json:"attempts"`
}

func (s *Store) UpsertStep(step *Step) error {
	_, err := s.db.Exec(`
		INSERT INTO steps (run_id, step_name, status, started_at, ended_at, error, attempts)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, step_name) DO UPDATE SET
			status=excluded.status,
			started_at=excluded.started_at,
			ended_at=excluded.ended_at,
			error=excluded.error,
			attempts=excluded.attempts
	`, step.RunID, step.StepName, step.Status,
		step.StartedAt, step.EndedAt, step.Error, step.Attempts,
	)
	return err
}

func (s *Store) ListSteps(runID string) ([]*Step, error) {
	rows, err := s.db.Query(
		`SELECT run_id, step_name, status, started_at, ended_at, error, attempts
		 FROM steps WHERE run_id=?`, runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []*Step
	for rows.Next() {
		var st Step
		var startedAt, endedAt sql.NullTime
		var errMsg sql.NullString
		if err := rows.Scan(
			&st.RunID, &st.StepName, &st.Status,
			&startedAt, &endedAt, &errMsg, &st.Attempts,
		); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			st.StartedAt = &startedAt.Time
		}
		if endedAt.Valid {
			st.EndedAt = &endedAt.Time
		}
		if errMsg.Valid {
			st.Error = errMsg.String
		}
		steps = append(steps, &st)
	}
	return steps, rows.Err()
}
