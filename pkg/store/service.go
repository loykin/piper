package store

import (
	"database/sql"
	"strings"
	"time"
)

// ServiceStatus constants
const (
	ServiceStatusRunning = "running"
	ServiceStatusStopped = "stopped"
	ServiceStatusFailed  = "failed"
)

// Service represents a deployed ModelService.
type Service struct {
	Name      string    `json:"name"`     // ModelService name (PK)
	RunID     string    `json:"run_id"`   // artifact's run ID currently being served
	Artifact  string    `json:"artifact"` // "step/artifact"
	Status    string    `json:"status"`   // running | stopped | failed
	Endpoint  string    `json:"endpoint"` // internal address (proxy target), e.g. "http://localhost:9001"
	PID       int       `json:"pid"`      // local mode: process PID (0 for k8s)
	YAML      string    `json:"yaml"`     // original YAML
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateService inserts a new Service record.
func (s *Store) CreateService(svc *Service) error {
	now := time.Now()
	svc.CreatedAt = now
	svc.UpdatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO services (name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		svc.Name, svc.RunID, svc.Artifact, svc.Status,
		svc.Endpoint, svc.PID, svc.YAML, svc.CreatedAt, svc.UpdatedAt,
	)
	return err
}

// GetService returns a Service by name.
func (s *Store) GetService(name string) (*Service, error) {
	row := s.db.QueryRow(`
		SELECT name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at
		FROM services WHERE name=?`, name)
	return scanService(row)
}

// UpdateService updates the mutable fields of a Service.
func (s *Store) UpdateService(svc *Service) error {
	svc.UpdatedAt = time.Now()
	_, err := s.db.Exec(`
		UPDATE services
		SET run_id=?, artifact=?, status=?, endpoint=?, pid=?, yaml=?, updated_at=?
		WHERE name=?`,
		svc.RunID, svc.Artifact, svc.Status, svc.Endpoint,
		svc.PID, svc.YAML, svc.UpdatedAt, svc.Name,
	)
	return err
}

// UpsertService inserts or replaces a Service record.
func (s *Store) UpsertService(svc *Service) error {
	now := time.Now()
	svc.UpdatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO services (name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			run_id     = excluded.run_id,
			artifact   = excluded.artifact,
			status     = excluded.status,
			endpoint   = excluded.endpoint,
			pid        = excluded.pid,
			yaml       = excluded.yaml,
			updated_at = excluded.updated_at`,
		svc.Name, svc.RunID, svc.Artifact, svc.Status,
		svc.Endpoint, svc.PID, svc.YAML, now, now,
	)
	return err
}

// SetServiceStatus updates only the status (and optionally PID/endpoint) of a Service.
func (s *Store) SetServiceStatus(name, status string) error {
	_, err := s.db.Exec(`
		UPDATE services SET status=?, updated_at=? WHERE name=?`,
		status, time.Now(), name,
	)
	return err
}

// ListServices returns all Service records ordered by created_at desc.
func (s *Store) ListServices() ([]*Service, error) {
	rows, err := s.db.Query(`
		SELECT name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at
		FROM services ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var svcs []*Service
	for rows.Next() {
		svc, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		svcs = append(svcs, svc)
	}
	return svcs, rows.Err()
}

// DeleteService removes a Service record by name.
func (s *Store) DeleteService(name string) error {
	_, err := s.db.Exec(`DELETE FROM services WHERE name=?`, name)
	return err
}

// GetLatestSuccessfulRun returns the most recent successful run for a given pipeline name.
// Returns (nil, nil) when no matching run exists.
func (s *Store) GetLatestSuccessfulRun(pipelineName string) (*Run, error) {
	row := s.db.QueryRow(`
		SELECT id, owner_id, pipeline_name, status, started_at, ended_at, scheduled_at
		FROM runs
		WHERE pipeline_name=? AND status='success'
		ORDER BY started_at DESC
		LIMIT 1`, pipelineName)
	r, err := scanRun(row)
	if err != nil {
		if err.Error() == sql.ErrNoRows.Error() || err == sql.ErrNoRows {
			return nil, nil
		}
		// scanRun wraps the error — check if it contains ErrNoRows
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

func isNoRows(err error) bool {
	if err == nil {
		return false
	}
	return err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows")
}

func scanService(s scanner) (*Service, error) {
	var svc Service
	err := s.Scan(
		&svc.Name, &svc.RunID, &svc.Artifact, &svc.Status,
		&svc.Endpoint, &svc.PID, &svc.YAML,
		&svc.CreatedAt, &svc.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}
