package store

import (
	"database/sql"
	"time"
)

// AgentStatus 상수
const (
	AgentStatusPending   = "pending"
	AgentStatusRunning   = "running"
	AgentStatusSucceeded = "succeeded"
	AgentStatusFailed    = "failed"
)

type Agent struct {
	ID        string     `json:"id"`
	TaskID    string     `json:"task_id"`
	RunID     string     `json:"run_id"`
	StepName  string     `json:"step_name"`
	Status    string     `json:"status"` // pending | running | succeeded | failed
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// CreateAgent는 Agent 레코드를 생성한다 (pending 상태).
func (s *Store) CreateAgent(a *Agent) error {
	_, err := s.db.Exec(`
		INSERT INTO agents (id, task_id, run_id, step_name, status)
		VALUES (?, ?, ?, ?, ?)
	`, a.ID, a.TaskID, a.RunID, a.StepName, AgentStatusPending)
	return err
}

// StartAgent는 Agent를 running 상태로 전이한다.
func (s *Store) StartAgent(id string) error {
	now := time.Now()
	_, err := s.db.Exec(`
		UPDATE agents SET status=?, started_at=? WHERE id=?
	`, AgentStatusRunning, now, id)
	return err
}

// FinishAgent는 Agent를 succeeded 또는 failed 상태로 전이한다.
func (s *Store) FinishAgent(id, status, errMsg string) error {
	now := time.Now()
	_, err := s.db.Exec(`
		UPDATE agents SET status=?, ended_at=?, error=? WHERE id=?
	`, status, now, errMsg, id)
	return err
}

// GetAgent는 단일 Agent를 반환한다.
func (s *Store) GetAgent(id string) (*Agent, error) {
	row := s.db.QueryRow(`
		SELECT id, task_id, run_id, step_name, status, started_at, ended_at, error
		FROM agents WHERE id=?
	`, id)
	return scanAgent(row)
}

// ListAgentsByRun은 특정 run의 Agent 목록을 반환한다.
func (s *Store) ListAgentsByRun(runID string) ([]*Agent, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, run_id, step_name, status, started_at, ended_at, error
		FROM agents WHERE run_id=? ORDER BY started_at ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var agents []*Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func scanAgent(s scanner) (*Agent, error) {
	var a Agent
	var startedAt, endedAt sql.NullTime
	var errMsg sql.NullString
	if err := s.Scan(
		&a.ID, &a.TaskID, &a.RunID, &a.StepName,
		&a.Status, &startedAt, &endedAt, &errMsg,
	); err != nil {
		return nil, err
	}
	if startedAt.Valid {
		a.StartedAt = &startedAt.Time
	}
	if endedAt.Valid {
		a.EndedAt = &endedAt.Time
	}
	if errMsg.Valid {
		a.Error = errMsg.String
	}
	return &a, nil
}
