package postgres

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/piper/piper/pkg/notebook"
)

type notebookRepo struct{ db *sqlx.DB }

// NewNotebookRepo returns a notebook.Repository backed by PostgreSQL.
func NewNotebookRepo(db *sqlx.DB) notebook.Repository { return &notebookRepo{db: db} }

const notebookSelectCols = `name, status, endpoint, pid, work_dir, token, image, namespace, created_at, updated_at`

func (r *notebookRepo) Create(ctx context.Context, nb *notebook.NotebookServer) error {
	now := time.Now()
	nb.CreatedAt = now
	nb.UpdatedAt = now
	q := r.db.Rebind(
		`INSERT INTO notebook_servers (name, status, endpoint, pid, work_dir, token, image, namespace, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q,
		nb.Name, nb.Status, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.Image, nb.Namespace,
		nb.CreatedAt, nb.UpdatedAt)
	return err
}

func (r *notebookRepo) Get(ctx context.Context, name string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	q := r.db.Rebind(`SELECT ` + notebookSelectCols + ` FROM notebook_servers WHERE name=?`)
	err := r.db.GetContext(ctx, &nb, q, name)
	if err != nil {
		return nil, err
	}
	return &nb, nil
}

func (r *notebookRepo) Update(ctx context.Context, nb *notebook.NotebookServer) error {
	nb.UpdatedAt = time.Now()
	q := r.db.Rebind(
		`UPDATE notebook_servers SET status=?, endpoint=?, pid=?, work_dir=?, token=?, image=?, namespace=?, updated_at=? WHERE name=?`)
	_, err := r.db.ExecContext(ctx, q,
		nb.Status, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.Image, nb.Namespace, nb.UpdatedAt, nb.Name)
	return err
}

func (r *notebookRepo) SetStatus(ctx context.Context, name, status string) error {
	q := r.db.Rebind(`UPDATE notebook_servers SET status=?, updated_at=? WHERE name=?`)
	_, err := r.db.ExecContext(ctx, q, status, time.Now(), name)
	return err
}

func (r *notebookRepo) List(ctx context.Context) ([]*notebook.NotebookServer, error) {
	var out []*notebook.NotebookServer
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+notebookSelectCols+` FROM notebook_servers ORDER BY created_at DESC`)
	if out == nil {
		out = []*notebook.NotebookServer{}
	}
	return out, err
}

func (r *notebookRepo) Delete(ctx context.Context, name string) error {
	q := r.db.Rebind(`DELETE FROM notebook_servers WHERE name=?`)
	_, err := r.db.ExecContext(ctx, q, name)
	return err
}
