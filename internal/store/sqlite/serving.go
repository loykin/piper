package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/piper/piper/pkg/serving"
)

type servingRepo struct{ db *sql.DB }

func NewServingRepo(db *sql.DB) serving.Repository { return &servingRepo{db: db} }

func (r *servingRepo) Create(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.CreatedAt = now
	svc.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO services (name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.PID, svc.YAML, svc.CreatedAt, svc.UpdatedAt)
	return err
}

func (r *servingRepo) Get(ctx context.Context, name string) (*serving.Service, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at FROM services WHERE name=?`, name)
	return scanService(row)
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
	rows, err := r.db.QueryContext(ctx,
		`SELECT name, run_id, artifact, status, endpoint, pid, yaml, created_at, updated_at FROM services ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*serving.Service
	for rows.Next() {
		svc, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

func (r *servingRepo) Delete(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM services WHERE name=?`, name)
	return err
}

func scanService(s interface{ Scan(dest ...any) error }) (*serving.Service, error) {
	var svc serving.Service
	err := s.Scan(&svc.Name, &svc.RunID, &svc.Artifact, &svc.Status,
		&svc.Endpoint, &svc.PID, &svc.YAML, &svc.CreatedAt, &svc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}
