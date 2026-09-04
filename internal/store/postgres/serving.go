package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/serving"
)

type servingRepo struct{ sqlxadapter.Source }

func NewServingRepo(exec *dbstore.Executor[*sqlx.DB], source string) serving.Repository {
	return &servingRepo{Source: sqlxadapter.NewSource(source, exec)}
}

const serviceSelectCols = `project_id, name, run_id, artifact, status, endpoint, namespace, pid, runtime_id, yaml, created_by, created_at, updated_at`

func (r *servingRepo) Create(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.CreatedAt = now
	svc.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO services (project_id, name, run_id, artifact, status, endpoint, namespace, pid, runtime_id, yaml, created_by, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q,
			svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.RuntimeID, svc.YAML, svc.CreatedBy, svc.CreatedAt, svc.UpdatedAt)
		return err
	})
}

func (r *servingRepo) Get(ctx context.Context, projectID, name string) (*serving.Service, error) {
	var svc serving.Service
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + serviceSelectCols + ` FROM services WHERE project_id=? AND name=?`)
		return db.GetContext(ctx, &svc, q, projectID, name)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

func (r *servingRepo) Update(ctx context.Context, svc *serving.Service) error {
	svc.UpdatedAt = time.Now()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE services SET run_id=?, artifact=?, status=?, endpoint=?, namespace=?, pid=?, runtime_id=?, yaml=?, updated_at=? WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q,
			svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.RuntimeID, svc.YAML, svc.UpdatedAt, svc.ProjectID, svc.Name)
		return err
	})
}

func (r *servingRepo) Upsert(ctx context.Context, svc *serving.Service) error {
	now := time.Now()
	svc.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO services (project_id, name, run_id, artifact, status, endpoint, namespace, pid, runtime_id, yaml, created_by, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(project_id, name) DO UPDATE SET
			 	run_id=EXCLUDED.run_id, artifact=EXCLUDED.artifact, status=EXCLUDED.status,
				endpoint=EXCLUDED.endpoint, namespace=EXCLUDED.namespace, pid=EXCLUDED.pid, runtime_id=EXCLUDED.runtime_id, yaml=EXCLUDED.yaml,
				created_by=EXCLUDED.created_by, updated_at=EXCLUDED.updated_at`)
		_, err := db.ExecContext(ctx, q,
			svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.RuntimeID, svc.YAML, svc.CreatedBy, now, now)
		return err
	})
}

func (r *servingRepo) SetStatus(ctx context.Context, projectID, name, status string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE services SET status=?, updated_at=? WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q, status, time.Now(), projectID, name)
		return err
	})
}

func (r *servingRepo) SetStatusEndpoint(ctx context.Context, projectID, name, status, endpoint string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE services
			SET status=?,
			    endpoint=CASE
			        WHEN ? IN (?, ?) THEN ''
			        WHEN ? <> '' THEN ?
			        ELSE endpoint
			    END,
			    pid=CASE WHEN ? IN (?, ?) THEN 0 ELSE pid END,
			    updated_at=?
			WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q,
			status,
			status, serving.StatusStopped, serving.StatusFailed,
			endpoint, endpoint,
			status, serving.StatusStopped, serving.StatusFailed,
			time.Now(), projectID, name)
		return err
	})
}

func (r *servingRepo) List(ctx context.Context, projectID string, limit, offset int) ([]*serving.Service, error) {
	query := `SELECT ` + serviceSelectCols + ` FROM services WHERE project_id=? ORDER BY created_at DESC`
	args := []any{projectID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var out []*serving.Service
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*serving.Service{}
	}
	return out, err
}

func (r *servingRepo) Count(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COUNT(*) FROM services WHERE project_id=?`)
		return db.GetContext(ctx, &count, q, projectID)
	})
	return count, err
}

// Delete archives the current row into service_history and removes it from
// services in a single transaction — see notebookRepo.Delete's doc comment
// for why a history-insert failure must not let the live row disappear
// silently.
func (r *servingRepo) Delete(ctx context.Context, projectID, name string) error {
	return r.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		var svc serving.Service
		q := tx.Rebind(`SELECT ` + serviceSelectCols + ` FROM services WHERE project_id=? AND name=?`)
		err := tx.GetContext(ctx, &svc, q, projectID, name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Nothing to archive; still remove the (already-absent) row below
			// so Delete stays idempotent, matching the prior behavior.
		case err != nil:
			return err
		default:
			if err := appendServiceHistory(ctx, tx, &svc); err != nil {
				return fmt.Errorf("append history: %w", err)
			}
		}
		q = tx.Rebind(`DELETE FROM services WHERE project_id=? AND name=?`)
		_, err = tx.ExecContext(ctx, q, projectID, name)
		return err
	})
}

// appendServiceHistory is shared by AppendHistory (its own transaction) and
// Delete (inside Delete's transaction) so both go through identical SQL.
// sqlx.ExtContext is satisfied by both *sqlx.DB and *sqlx.Tx.
func appendServiceHistory(ctx context.Context, execer sqlx.ExtContext, svc *serving.Service) error {
	q := execer.Rebind(`INSERT INTO service_history (project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, created_by, deployed_at, stopped_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := execer.ExecContext(ctx, q,
		svc.ProjectID, svc.Name, svc.RunID, svc.Artifact, svc.Status, svc.Endpoint, svc.Namespace, svc.PID, svc.YAML, svc.CreatedBy, svc.CreatedAt, time.Now())
	return err
}

func (r *servingRepo) AppendHistory(ctx context.Context, svc *serving.Service) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return appendServiceHistory(ctx, db, svc)
	})
}

func (r *servingRepo) ListHistory(ctx context.Context, projectID string, limit, offset int) ([]*serving.ServiceHistory, error) {
	query := `SELECT id, project_id, name, run_id, artifact, status, endpoint, namespace, pid, yaml, created_by, deployed_at, stopped_at
		 FROM service_history WHERE project_id=? ORDER BY stopped_at DESC`
	args := []any{projectID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var out []*serving.ServiceHistory
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*serving.ServiceHistory{}
	}
	return out, err
}

func (r *servingRepo) CountHistory(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COUNT(*) FROM service_history WHERE project_id=?`)
		return db.GetContext(ctx, &count, q, projectID)
	})
	return count, err
}
