package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/serving"
)

type servingRepo struct{ dbstore.BaseRepo }

func NewServingRepo(exec *dbstore.Executor, source string) serving.Repository {
	return &servingRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

const serviceSelectCols = `project_id, name, run_id, artifact, status, endpoint, namespace, pid, worker_id, yaml, created_at, updated_at`

func (r *servingRepo) Create(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.CreatedAt = now
	svc.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO services (project_id, name, run_id, artifact, status, endpoint, namespace, pid, worker_id, yaml, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.WorkerID, svc.YAML, svc.CreatedAt, svc.UpdatedAt)
		return err
	})
}

func (r *servingRepo) Get(ctx context.Context, projectID, name string) (*serving.Service, error) {
	var svc serving.Service
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &svc,
			`SELECT `+serviceSelectCols+` FROM services WHERE project_id=? AND name=?`, projectID, name)
	})
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

func (r *servingRepo) Update(ctx context.Context, svc *serving.Service) error {
	svc.UpdatedAt = time.Now()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE services SET run_id=?, artifact=?, status=?, endpoint=?, namespace=?, pid=?, worker_id=?, yaml=?, updated_at=? WHERE project_id=? AND name=?`,
			svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.WorkerID, svc.YAML, svc.UpdatedAt, svc.ProjectID, svc.Name)
		return err
	})
}

func (r *servingRepo) Upsert(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO services (project_id, name, run_id, artifact, status, endpoint, namespace, pid, worker_id, yaml, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(project_id, name) DO UPDATE SET
			 	run_id=excluded.run_id, artifact=excluded.artifact, status=excluded.status,
			 	endpoint=excluded.endpoint, namespace=excluded.namespace, pid=excluded.pid, worker_id=excluded.worker_id, yaml=excluded.yaml, updated_at=excluded.updated_at`,
			svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.WorkerID, svc.YAML, now, now)
		return err
	})
}

func (r *servingRepo) SetStatus(ctx context.Context, projectID, name, status string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE services SET status=?, updated_at=? WHERE project_id=? AND name=?`, status, time.Now(), projectID, name)
		return err
	})
}

func (r *servingRepo) SetStatusEndpoint(ctx context.Context, projectID, name, status, endpoint string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE services
			 SET status=?,
			     endpoint=CASE
			         WHEN ? IN (?, ?) THEN ''
			         WHEN ? <> '' THEN ?
			         ELSE endpoint
			     END,
			     pid=CASE WHEN ? IN (?, ?) THEN 0 ELSE pid END,
			     updated_at=?
			 WHERE project_id=? AND name=?`,
			status,
			status, serving.StatusStopped, serving.StatusFailed,
			endpoint, endpoint,
			status, serving.StatusStopped, serving.StatusFailed,
			time.Now(), projectID, name)
		return err
	})
}

func (r *servingRepo) List(ctx context.Context, projectID string) ([]*serving.Service, error) {
	var out []*serving.Service
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT `+serviceSelectCols+` FROM services WHERE project_id=? ORDER BY created_at DESC`, projectID)
	})
	if out == nil {
		out = []*serving.Service{}
	}
	return out, err
}

func (r *servingRepo) ListByWorker(ctx context.Context, workerID string) ([]*serving.Service, error) {
	var out []*serving.Service
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT `+serviceSelectCols+` FROM services WHERE worker_id=? ORDER BY created_at DESC`, workerID)
	})
	return out, err
}

func (r *servingRepo) Delete(ctx context.Context, projectID, name string) error {
	var svc serving.Service
	if err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &svc,
			`SELECT `+serviceSelectCols+` FROM services WHERE project_id=? AND name=?`, projectID, name)
	}); err == nil {
		_ = r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
			_, err := db.ExecContext(ctx,
				`INSERT INTO service_history (project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, deployed_at, stopped_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.YAML, svc.CreatedAt, time.Now())
			return err
		})
	}
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM services WHERE project_id=? AND name=?`, projectID, name)
		return err
	})
}

func (r *servingRepo) ListHistory(ctx context.Context, projectID string) ([]*serving.ServiceHistory, error) {
	var out []*serving.ServiceHistory
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT id, project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, deployed_at, stopped_at
			 FROM service_history WHERE project_id=? ORDER BY stopped_at DESC`, projectID)
	})
	if out == nil {
		out = []*serving.ServiceHistory{}
	}
	return out, err
}
