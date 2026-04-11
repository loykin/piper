package store

import (
	"database/sql"
	"time"
)

// WorkerStatus 상수
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
	InFlight     int       `json:"in_flight"` // 현재 실행 중인 태스크 수
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// UpsertWorker는 Worker를 등록하거나 재등록 시 갱신한다.
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

// HeartbeatWorker는 Worker의 last_seen_at과 in_flight를 갱신한다.
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

// SetWorkerStatus는 Worker 상태를 변경한다 (draining, offline 등).
func (s *Store) SetWorkerStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE workers SET status=? WHERE id=?`, status, id)
	return err
}

// GetWorker는 단일 Worker를 반환한다.
func (s *Store) GetWorker(id string) (*Worker, error) {
	row := s.db.QueryRow(`
		SELECT id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at
		FROM workers WHERE id=?
	`, id)
	return scanWorker(row)
}

// ListWorkers는 전체 Worker 목록을 반환한다.
// onlineOnly=true면 status='online' 또는 'draining'인 것만 반환.
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

// MarkWorkersOffline은 last_seen_at이 before 이전인 online Worker를 offline으로 만든다.
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
