package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/piper/piper/pkg/notebook"
)

type notebookRepo struct{ db *sqlx.DB }

// NewNotebookRepo returns a notebook.Repository backed by SQLite.
func NewNotebookRepo(db *sqlx.DB) notebook.Repository { return &notebookRepo{db: db} }

const notebookCols = `name, status, env, endpoint, pid, work_dir, token, worker_id, volume_id, image, yaml, created_at, updated_at`

func (r *notebookRepo) Create(ctx context.Context, nb *notebook.NotebookServer) error {
	now := time.Now()
	nb.CreatedAt = now
	nb.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notebook_servers (name, status, env, endpoint, pid, work_dir, token, worker_id, volume_id, image, yaml, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.WorkerID, nb.VolumeID, nb.Image, nb.YAML,
		nb.CreatedAt, nb.UpdatedAt)
	return err
}

func (r *notebookRepo) Get(ctx context.Context, name string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	err := r.db.GetContext(ctx, &nb,
		`SELECT `+notebookCols+` FROM notebook_servers WHERE name=?`, name)
	if err != nil {
		return nil, err
	}
	return &nb, nil
}

func (r *notebookRepo) Update(ctx context.Context, nb *notebook.NotebookServer) error {
	nb.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE notebook_servers SET status=?, env=?, endpoint=?, pid=?, work_dir=?, token=?, worker_id=?, volume_id=?, image=?, yaml=?, updated_at=? WHERE name=?`,
		nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.WorkerID, nb.VolumeID, nb.Image, nb.YAML, nb.UpdatedAt, nb.Name)
	return err
}

func (r *notebookRepo) SetStatus(ctx context.Context, name, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE notebook_servers SET status=?, updated_at=? WHERE name=?`, status, time.Now(), name)
	return err
}

func (r *notebookRepo) List(ctx context.Context) ([]*notebook.NotebookServer, error) {
	var out []*notebook.NotebookServer
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+notebookCols+` FROM notebook_servers ORDER BY created_at DESC`)
	if out == nil {
		out = []*notebook.NotebookServer{}
	}
	return out, err
}

func (r *notebookRepo) Delete(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM notebook_servers WHERE name=?`, name)
	return err
}
