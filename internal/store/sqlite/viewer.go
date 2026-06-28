package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/viewer"
)

type viewerRepo struct{ dbstore.BaseRepo }

func NewViewerRepo(exec *dbstore.Executor, source string) viewer.Repository {
	return &viewerRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *viewerRepo) Create(ctx context.Context, v *viewer.Viewer) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `
			INSERT INTO viewers
			  (id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.ID, v.ProjectID, v.Type, v.RunID, v.StepName, v.Artifact,
			string(v.Status), v.Endpoint, v.PID, v.WorkDir,
			v.CreatedAt, v.UpdatedAt, v.ExpiresAt,
		)
		return err
	})
}

func (r *viewerRepo) Get(ctx context.Context, id string) (*viewer.Viewer, error) {
	var v viewer.Viewer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &v,
			`SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
			 FROM viewers WHERE id=?`, id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("viewer not found")
	}
	return &v, err
}

func (r *viewerRepo) List(ctx context.Context, projectID string) ([]*viewer.Viewer, error) {
	var vs []*viewer.Viewer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &vs,
			`SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
			 FROM viewers WHERE project_id=? ORDER BY created_at DESC`, projectID)
	})
	if vs == nil {
		vs = []*viewer.Viewer{}
	}
	return vs, err
}

func (r *viewerRepo) FindRunning(ctx context.Context, projectID, runID, stepName, artifact, typ string) (*viewer.Viewer, error) {
	var v viewer.Viewer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &v,
			`SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
			 FROM viewers
			 WHERE project_id=? AND run_id=? AND step_name=? AND artifact=? AND type=?
			   AND status='running'
			 LIMIT 1`,
			projectID, runID, stepName, artifact, typ)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

func (r *viewerRepo) UpdateStatus(ctx context.Context, id string, status viewer.Status, endpoint string, pid int, workDir string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE viewers SET status=?, endpoint=?, pid=?, work_dir=?, updated_at=? WHERE id=?`,
			string(status), endpoint, pid, workDir, time.Now().UTC(), id)
		return err
	})
}

func (r *viewerRepo) ListExpired(ctx context.Context) ([]*viewer.Viewer, error) {
	var vs []*viewer.Viewer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &vs,
			`SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
			 FROM viewers WHERE expires_at IS NOT NULL AND expires_at < ?`, time.Now().UTC())
	})
	return vs, err
}

func (r *viewerRepo) MarkStaleFailed(ctx context.Context) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE viewers SET status='failed', updated_at=? WHERE status IN ('starting','running')`,
			time.Now().UTC())
		return err
	})
}

func (r *viewerRepo) Delete(ctx context.Context, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM viewers WHERE id=?`, id)
		return err
	})
}
