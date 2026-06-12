package postgres

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/serving"
)

type servingRepo struct{ db *sqlx.DB }

func NewServingRepo(db *sqlx.DB) serving.Repository { return &servingRepo{db: db} }

const serviceSelectCols = `project_id, name, run_id, artifact, status, endpoint, namespace, pid, worker_id, yaml, created_at, updated_at`

func (r *servingRepo) Create(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.CreatedAt = now
	svc.UpdatedAt = now
	q := r.db.Rebind(`INSERT INTO services (project_id, name, run_id, artifact, status, endpoint, namespace, pid, worker_id, yaml, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.WorkerID, svc.YAML, svc.CreatedAt, svc.UpdatedAt)
	return err
}

func (r *servingRepo) Get(ctx context.Context, projectID, name string) (*serving.Service, error) {
	var svc serving.Service
	q := r.db.Rebind(`SELECT ` + serviceSelectCols + ` FROM services WHERE project_id=? AND name=?`)
	err := r.db.GetContext(ctx, &svc, q, projectID, name)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

func (r *servingRepo) Update(ctx context.Context, svc *serving.Service) error {
	svc.UpdatedAt = time.Now()
	q := r.db.Rebind(`UPDATE services SET run_id=?, artifact=?, status=?, endpoint=?, namespace=?, pid=?, worker_id=?, yaml=?, updated_at=? WHERE project_id=? AND name=?`)
	_, err := r.db.ExecContext(ctx, q,
		svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.WorkerID, svc.YAML, svc.UpdatedAt, svc.ProjectID, svc.Name)
	return err
}

func (r *servingRepo) Upsert(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.UpdatedAt = now
	q := r.db.Rebind(`INSERT INTO services (project_id, name, run_id, artifact, status, endpoint, namespace, pid, worker_id, yaml, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, name) DO UPDATE SET
		run_id=EXCLUDED.run_id, artifact=EXCLUDED.artifact, status=EXCLUDED.status,
		 	endpoint=EXCLUDED.endpoint, namespace=EXCLUDED.namespace, pid=EXCLUDED.pid, worker_id=EXCLUDED.worker_id, yaml=EXCLUDED.yaml, updated_at=EXCLUDED.updated_at`)
	_, err := r.db.ExecContext(ctx, q,
		svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.WorkerID, svc.YAML, now, now)
	return err
}

func (r *servingRepo) SetStatus(ctx context.Context, projectID, name, status string) error {
	q := r.db.Rebind(`UPDATE services SET status=?, updated_at=? WHERE project_id=? AND name=?`)
	_, err := r.db.ExecContext(ctx, q, status, time.Now(), projectID, name)
	return err
}

func (r *servingRepo) SetStatusEndpoint(ctx context.Context, projectID, name, status, endpoint string) error {
	q := r.db.Rebind(`UPDATE services
		SET status=?,
		    endpoint=CASE
		        WHEN ? IN (?, ?) THEN ''
		        WHEN ? <> '' THEN ?
		        ELSE endpoint
		    END,
		    pid=CASE WHEN ? IN (?, ?) THEN 0 ELSE pid END,
		    updated_at=?
		WHERE project_id=? AND name=?`)
	_, err := r.db.ExecContext(ctx, q,
		status,
		status, serving.StatusStopped, serving.StatusFailed,
		endpoint, endpoint,
		status, serving.StatusStopped, serving.StatusFailed,
		time.Now(), projectID, name)
	return err
}

func (r *servingRepo) List(ctx context.Context, projectID string) ([]*serving.Service, error) {
	var out []*serving.Service
	q := r.db.Rebind(`SELECT ` + serviceSelectCols + ` FROM services WHERE project_id=? ORDER BY created_at DESC`)
	err := r.db.SelectContext(ctx, &out, q, projectID)
	if out == nil {
		out = []*serving.Service{}
	}
	return out, err
}

func (r *servingRepo) ListByWorker(ctx context.Context, workerID string) ([]*serving.Service, error) {
	var out []*serving.Service
	q := r.db.Rebind(`SELECT ` + serviceSelectCols + ` FROM services WHERE worker_id=? ORDER BY created_at DESC`)
	err := r.db.SelectContext(ctx, &out, q, workerID)
	return out, err
}

func (r *servingRepo) Delete(ctx context.Context, projectID, name string) error {
	// Archive to history before removing.
	var svc serving.Service
	qGet := r.db.Rebind(`SELECT ` + serviceSelectCols + ` FROM services WHERE project_id=? AND name=?`)
	if err := r.db.GetContext(ctx, &svc, qGet, projectID, name); err == nil {
		qIns := r.db.Rebind(`INSERT INTO service_history (project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, deployed_at, stopped_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		_, _ = r.db.ExecContext(ctx, qIns,
			svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.YAML, svc.CreatedAt, time.Now())
	}
	q := r.db.Rebind(`DELETE FROM services WHERE project_id=? AND name=?`)
	_, err := r.db.ExecContext(ctx, q, projectID, name)
	return err
}

func (r *servingRepo) ListHistory(ctx context.Context, projectID string) ([]*serving.ServiceHistory, error) {
	var out []*serving.ServiceHistory
	q := r.db.Rebind(`SELECT id, project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, deployed_at, stopped_at
		 FROM service_history WHERE project_id=? ORDER BY stopped_at DESC`)
	err := r.db.SelectContext(ctx, &out, q, projectID)
	if out == nil {
		out = []*serving.ServiceHistory{}
	}
	return out, err
}
