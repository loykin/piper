package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/piper/piper/pkg/notebook"
)

type notebookVolumeRepo struct{ db *sqlx.DB }

// NewNotebookVolumeRepo returns a notebook.VolumeRepository backed by SQLite.
func NewNotebookVolumeRepo(db *sqlx.DB) notebook.VolumeRepository {
	return &notebookVolumeRepo{db: db}
}

const volumeCols = `project_id, id, label, work_dir, status, worker_id, created_at, updated_at`

func (r *notebookVolumeRepo) Create(ctx context.Context, v *notebook.NotebookVolume) error {
	now := time.Now()
	v.CreatedAt = now
	v.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notebook_volumes (project_id, id, label, work_dir, status, worker_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ProjectID, v.ID, v.Label, v.WorkDir, v.Status, v.WorkerID, v.CreatedAt, v.UpdatedAt)
	return err
}

func (r *notebookVolumeRepo) Get(ctx context.Context, id string) (*notebook.NotebookVolume, error) {
	var v notebook.NotebookVolume
	err := r.db.GetContext(ctx, &v,
		`SELECT `+volumeCols+` FROM notebook_volumes WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *notebookVolumeRepo) List(ctx context.Context, projectID string) ([]*notebook.NotebookVolume, error) {
	var out []*notebook.NotebookVolume
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+volumeCols+` FROM notebook_volumes WHERE project_id=? ORDER BY created_at DESC`, projectID)
	if out == nil {
		out = []*notebook.NotebookVolume{}
	}
	return out, err
}

func (r *notebookVolumeRepo) Update(ctx context.Context, v *notebook.NotebookVolume) error {
	v.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE notebook_volumes SET label=?, work_dir=?, status=?, worker_id=?, updated_at=? WHERE id=?`,
		v.Label, v.WorkDir, v.Status, v.WorkerID, v.UpdatedAt, v.ID)
	return err
}

func (r *notebookVolumeRepo) SetStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE notebook_volumes SET status=?, updated_at=? WHERE id=?`,
		status, time.Now(), id)
	return err
}

func (r *notebookVolumeRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM notebook_volumes WHERE id=?`, id)
	return err
}
