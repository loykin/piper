package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/viewer"
)

type viewerRepo struct{ db *sqlx.DB }

func NewViewerRepo(db *sqlx.DB) viewer.Repository { return &viewerRepo{db: db} }

func (r *viewerRepo) Create(ctx context.Context, v *viewer.Viewer) error {
	q := r.db.Rebind(`
		INSERT INTO viewers
		  (id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		v.ID, v.ProjectID, v.Type, v.RunID, v.StepName, v.Artifact,
		string(v.Status), v.Endpoint, v.PID, v.WorkDir,
		v.CreatedAt, v.UpdatedAt, v.ExpiresAt,
	)
	return err
}

func (r *viewerRepo) Get(ctx context.Context, id string) (*viewer.Viewer, error) {
	var v viewer.Viewer
	q := r.db.Rebind(`
		SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
		FROM viewers WHERE id=?`)
	err := r.db.GetContext(ctx, &v, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("viewer not found")
	}
	return &v, err
}

func (r *viewerRepo) List(ctx context.Context, projectID string) ([]*viewer.Viewer, error) {
	var vs []*viewer.Viewer
	q := r.db.Rebind(`
		SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
		FROM viewers WHERE project_id=? ORDER BY created_at DESC`)
	err := r.db.SelectContext(ctx, &vs, q, projectID)
	if vs == nil {
		vs = []*viewer.Viewer{}
	}
	return vs, err
}

func (r *viewerRepo) FindRunning(ctx context.Context, projectID, runID, stepName, artifact, typ string) (*viewer.Viewer, error) {
	var v viewer.Viewer
	q := r.db.Rebind(`
		SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
		FROM viewers
		WHERE project_id=? AND run_id=? AND step_name=? AND artifact=? AND type=? AND status='running'
		LIMIT 1`)
	err := r.db.GetContext(ctx, &v, q, projectID, runID, stepName, artifact, typ)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

func (r *viewerRepo) UpdateStatus(ctx context.Context, id string, status viewer.Status, endpoint string, pid int, workDir string) error {
	q := r.db.Rebind(`UPDATE viewers SET status=?, endpoint=?, pid=?, work_dir=?, updated_at=? WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, string(status), endpoint, pid, workDir, time.Now().UTC(), id)
	return err
}

func (r *viewerRepo) ListExpired(ctx context.Context) ([]*viewer.Viewer, error) {
	var vs []*viewer.Viewer
	q := r.db.Rebind(`
		SELECT id, project_id, type, run_id, step_name, artifact, status, endpoint, pid, work_dir, created_at, updated_at, expires_at
		FROM viewers WHERE expires_at IS NOT NULL AND expires_at < ?`)
	err := r.db.SelectContext(ctx, &vs, q, time.Now().UTC())
	return vs, err
}

func (r *viewerRepo) MarkStaleFailed(ctx context.Context) error {
	q := r.db.Rebind(`UPDATE viewers SET status='failed', updated_at=? WHERE status IN ('starting','running')`)
	_, err := r.db.ExecContext(ctx, q, time.Now().UTC())
	return err
}

func (r *viewerRepo) Delete(ctx context.Context, id string) error {
	q := r.db.Rebind(`DELETE FROM viewers WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, id)
	return err
}
