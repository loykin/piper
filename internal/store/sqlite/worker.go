package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/internal/worker"
)

type workerRepo struct{ db *sqlx.DB }

func NewWorkerRepo(db *sqlx.DB) worker.Repository { return &workerRepo{db: db} }

const workerSelectCols = `id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at`

func (r *workerRepo) Upsert(ctx context.Context, w *worker.WorkerRecord) error {
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO workers (id, label, hostname, concurrency, status, in_flight, registered_at, last_seen_at)
		VALUES (:id, :label, :hostname, :concurrency, :status, :in_flight, :registered_at, :last_seen_at)
		ON CONFLICT(id) DO UPDATE SET
			label=excluded.label, hostname=excluded.hostname, concurrency=excluded.concurrency,
			status=excluded.status, in_flight=excluded.in_flight,
			registered_at=excluded.registered_at, last_seen_at=excluded.last_seen_at
	`, w)
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
	var w worker.WorkerRecord
	err := r.db.GetContext(ctx, &w,
		`SELECT `+workerSelectCols+` FROM workers WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *workerRepo) List(ctx context.Context, onlineOnly bool) ([]*worker.WorkerRecord, error) {
	query := `SELECT ` + workerSelectCols + ` FROM workers`
	if onlineOnly {
		query += ` WHERE status IN ('online', 'draining')`
	}
	query += ` ORDER BY registered_at DESC`
	var out []*worker.WorkerRecord
	err := r.db.SelectContext(ctx, &out, query)
	if out == nil {
		out = []*worker.WorkerRecord{}
	}
	return out, err
}

func (r *workerRepo) MarkOffline(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE workers SET status='offline' WHERE status='online' AND last_seen_at < ?`, before)
	return err
}
