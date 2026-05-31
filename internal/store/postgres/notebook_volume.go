package postgres

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/piper/piper/pkg/notebook"
)

type notebookVolumeRepo struct{ db *sqlx.DB }

// NewNotebookVolumeRepo returns a notebook.VolumeRepository backed by PostgreSQL.
func NewNotebookVolumeRepo(db *sqlx.DB) notebook.VolumeRepository {
	return &notebookVolumeRepo{db: db}
}

const pgVolumeCols = `id, label, work_dir, status, worker_id, created_at, updated_at`

func (r *notebookVolumeRepo) Create(ctx context.Context, v *notebook.NotebookVolume) error {
	now := time.Now()
	v.CreatedAt = now
	v.UpdatedAt = now
	q := r.db.Rebind(
		`INSERT INTO notebook_volumes (id, label, work_dir, status, worker_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		v.ID, v.Label, v.WorkDir, v.Status, v.WorkerID, v.CreatedAt, v.UpdatedAt)
	return err
}

func (r *notebookVolumeRepo) Get(ctx context.Context, id string) (*notebook.NotebookVolume, error) {
	var v notebook.NotebookVolume
	q := r.db.Rebind(`SELECT ` + pgVolumeCols + ` FROM notebook_volumes WHERE id=?`)
	err := r.db.GetContext(ctx, &v, q, id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *notebookVolumeRepo) List(ctx context.Context) ([]*notebook.NotebookVolume, error) {
	var out []*notebook.NotebookVolume
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+pgVolumeCols+` FROM notebook_volumes ORDER BY created_at DESC`)
	if out == nil {
		out = []*notebook.NotebookVolume{}
	}
	return out, err
}

func (r *notebookVolumeRepo) Update(ctx context.Context, v *notebook.NotebookVolume) error {
	v.UpdatedAt = time.Now()
	q := r.db.Rebind(`UPDATE notebook_volumes SET label=?, work_dir=?, status=?, worker_id=?, updated_at=? WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, v.Label, v.WorkDir, v.Status, v.WorkerID, v.UpdatedAt, v.ID)
	return err
}

func (r *notebookVolumeRepo) SetStatus(ctx context.Context, id, status string) error {
	q := r.db.Rebind(`UPDATE notebook_volumes SET status=?, updated_at=? WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, status, time.Now(), id)
	return err
}

func (r *notebookVolumeRepo) Delete(ctx context.Context, id string) error {
	q := r.db.Rebind(`DELETE FROM notebook_volumes WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, id)
	return err
}
