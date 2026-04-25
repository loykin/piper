package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/piper/piper/pkg/worker"
)

type workerRepo struct{ db *sql.DB }

func NewWorkerRepo(db *sql.DB) worker.Repository { return &workerRepo{db: db} }

func (r *workerRepo) Upsert(ctx context.Context, w *worker.WorkerRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workers (id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			label=excluded.label, hostname=excluded.hostname, concurrency=excluded.concurrency,
			status=excluded.status, in_flight=excluded.in_flight,
			registered_at=excluded.registered_at, last_seen_at=excluded.last_seen_at
	`, w.ID, w.Label, w.Hostname, w.Concurrency, w.Status, w.InFlight, w.RegisteredAt, w.LastSeenAt)
	return err
}

func (r *workerRepo) Heartbeat(ctx context.Context, id string, inFlight int) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE workers SET last_seen_at=?, in_flight=? WHERE id=?`, time.Now(), inFlight, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *workerRepo) SetStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE workers SET status=? WHERE id=?`, status, id)
	return err
}

func (r *workerRepo) Get(ctx context.Context, id string) (*worker.WorkerRecord, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at FROM workers WHERE id=?`, id)
	return scanWorker(row)
}

func (r *workerRepo) List(ctx context.Context, onlineOnly bool) ([]*worker.WorkerRecord, error) {
	query := `SELECT id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at FROM workers`
	if onlineOnly {
		query += ` WHERE status IN ('online', 'draining')`
	}
	query += ` ORDER BY registered_at DESC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*worker.WorkerRecord
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *workerRepo) MarkOffline(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE workers SET status='offline' WHERE status='online' AND last_seen_at < ?`, before)
	return err
}

func scanWorker(s interface{ Scan(dest ...any) error }) (*worker.WorkerRecord, error) {
	var w worker.WorkerRecord
	err := s.Scan(&w.ID, &w.Label, &w.Hostname, &w.Concurrency,
		&w.Status, &w.InFlight, &w.RegisteredAt, &w.LastSeenAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}
