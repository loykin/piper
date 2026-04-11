package store

import (
	"database/sql"
	"time"
)

// WorkerStatus constants
const (
	WorkerStatusOnline   = "online"
	WorkerStatusDraining = "draining"
	WorkerStatusOffline  = "offline"
)

type Worker struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Hostname     string    `json:"hostname"`
	Concurrency  int       `json:"concurrency"`
	Status       string    `json:"status"`    // online | draining | offline
	InFlight     int       `json:"in_flight"` // number of tasks currently in flight
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// UpsertWorker registers a Worker or updates it on re-registration.
func (s *Store) UpsertWorker(w *Worker) error {
	_, err := s.db.Exec(`
		INSERT INTO workers (id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			label         = excluded.label,
			hostname      = excluded.hostname,
			concurrency   = excluded.concurrency,
			status        = excluded.status,
			in_flight     = excluded.in_flight,
			registered_at = excluded.registered_at,
			last_seen_at  = excluded.last_seen_at
	`, w.ID, w.Label, w.Hostname, w.Concurrency,
		w.Status, w.InFlight, w.RegisteredAt, w.LastSeenAt,
	)
	return err
}

// HeartbeatWorker updates last_seen_at and in_flight for a Worker.
func (s *Store) HeartbeatWorker(id string, inFlight int) error {
	res, err := s.db.Exec(`
		UPDATE workers SET last_seen_at=?, in_flight=? WHERE id=?
	`, time.Now(), inFlight, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetWorkerStatus changes the status of a Worker (draining, offline, etc.).
func (s *Store) SetWorkerStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE workers SET status=? WHERE id=?`, status, id)
	return err
}

// GetWorker returns a single Worker by ID.
func (s *Store) GetWorker(id string) (*Worker, error) {
	row := s.db.QueryRow(`
		SELECT id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at
		FROM workers WHERE id=?
	`, id)
	return scanWorker(row)
}

// ListWorkers returns the full list of Workers.
// If onlineOnly is true, only workers with status 'online' or 'draining' are returned.
func (s *Store) ListWorkers(onlineOnly bool) ([]*Worker, error) {
	query := `
		SELECT id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at
		FROM workers`
	if onlineOnly {
		query += ` WHERE status IN ('online', 'draining')`
	}
	query += ` ORDER BY registered_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var workers []*Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

// MarkWorkersOffline marks online Workers whose last_seen_at is before the given time as offline.
func (s *Store) MarkWorkersOffline(before time.Time) error {
	_, err := s.db.Exec(`
		UPDATE workers SET status='offline'
		WHERE status='online' AND last_seen_at < ?
	`, before)
	return err
}

func scanWorker(s scanner) (*Worker, error) {
	var w Worker
	err := s.Scan(
		&w.ID, &w.Label, &w.Hostname, &w.Concurrency,
		&w.Status, &w.InFlight, &w.RegisteredAt, &w.LastSeenAt,
	)
	if err != nil {
		return nil, err
	}
	return &w, nil
}
