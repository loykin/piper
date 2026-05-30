package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/internal/worker"
)

type workerRepo struct{ db *sqlx.DB }

func NewWorkerRepo(db *sqlx.DB) worker.Repository { return &workerRepo{db: db} }

const workerSelectCols = `id, label, version, capabilities, hostname, concurrency, status, in_flight, registered_at, last_seen_at`

func (r *workerRepo) Upsert(ctx context.Context, w *worker.WorkerRecord) error {
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO workers (id, label, version, capabilities, hostname, concurrency, status, in_flight, registered_at, last_seen_at)
		VALUES (:id, :label, :version, :capabilities, :hostname, :concurrency, :status, :in_flight, :registered_at, :last_seen_at)
		ON CONFLICT(id) DO UPDATE SET
			label=EXCLUDED.label, version=EXCLUDED.version, capabilities=EXCLUDED.capabilities,
			hostname=EXCLUDED.hostname, concurrency=EXCLUDED.concurrency,
			status=EXCLUDED.status, in_flight=EXCLUDED.in_flight,
			registered_at=EXCLUDED.registered_at, last_seen_at=EXCLUDED.last_seen_at
	`, w)
	return err
}

func (r *workerRepo) Heartbeat(ctx context.Context, id string, inFlight int) error {
	q := r.db.Rebind(`UPDATE workers SET last_seen_at=?, in_flight=? WHERE id=?`)
	res, err := r.db.ExecContext(ctx, q, time.Now(), inFlight, id)
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
	q := r.db.Rebind(`UPDATE workers SET status=? WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, status, id)
	return err
}

func (r *workerRepo) Get(ctx context.Context, id string) (*worker.WorkerRecord, error) {
	var w worker.WorkerRecord
	q := r.db.Rebind(`SELECT ` + workerSelectCols + ` FROM workers WHERE id=?`)
	err := r.db.GetContext(ctx, &w, q, id)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *workerRepo) List(ctx context.Context, onlineOnly bool) ([]*worker.WorkerRecord, error) {
	var query string
	if onlineOnly {
		query = `SELECT ` + workerSelectCols + ` FROM workers WHERE status IN ('online', 'draining') ORDER BY registered_at DESC`
	} else {
		query = `SELECT ` + workerSelectCols + ` FROM workers ORDER BY registered_at DESC`
	}
	var out []*worker.WorkerRecord
	err := r.db.SelectContext(ctx, &out, query)
	if out == nil {
		out = []*worker.WorkerRecord{}
	}
	return out, err
}

func (r *workerRepo) MarkOffline(ctx context.Context, before time.Time) error {
	q := r.db.Rebind(`UPDATE workers SET status='offline' WHERE status='online' AND last_seen_at < ?`)
	_, err := r.db.ExecContext(ctx, q, before)
	return err
}
