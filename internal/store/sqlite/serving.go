package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/serving"
)

type servingRepo struct{ db *sqlx.DB }

func NewServingRepo(db *sqlx.DB) serving.Repository { return &servingRepo{db: db} }

const serviceSelectCols = `name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at`

func (r *servingRepo) Create(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.CreatedAt = now
	svc.UpdatedAt = now
	_, err := r.db.NamedExecContext(ctx,
		`INSERT INTO services (name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at)
		 VALUES (:name, :run_id, :artifact, :status, :endpoint, :pid, :yaml, :created_at, :updated_at)`,
		svc)
	return err
}

func (r *servingRepo) Get(ctx context.Context, name string) (*serving.Service, error) {
	var svc serving.Service
	err := r.db.GetContext(ctx, &svc,
		`SELECT `+serviceSelectCols+` FROM services WHERE name=?`, name)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

func (r *servingRepo) Update(ctx context.Context, svc *serving.Service) error {
	svc.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE services SET run_id=?, artifact=?, status=?, endpoint=?, pid=?, yaml=?, updated_at=? WHERE name=?`,
		svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.PID, svc.YAML, svc.UpdatedAt, svc.Name)
	return err
}

func (r *servingRepo) Upsert(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO services (name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		 	run_id=excluded.run_id, artifact=excluded.artifact, status=excluded.status,
		 	endpoint=excluded.endpoint, pid=excluded.pid, yaml=excluded.yaml, updated_at=excluded.updated_at`,
		svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.PID, svc.YAML, now, now)
	return err
}

func (r *servingRepo) SetStatus(ctx context.Context, name, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE services SET status=?, updated_at=? WHERE name=?`, status, time.Now(), name)
	return err
}

func (r *servingRepo) List(ctx context.Context) ([]*serving.Service, error) {
	var out []*serving.Service
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+serviceSelectCols+` FROM services ORDER BY created_at DESC`)
	if out == nil {
		out = []*serving.Service{}
	}
	return out, err
}

func (r *servingRepo) Delete(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM services WHERE name=?`, name)
	return err
}
