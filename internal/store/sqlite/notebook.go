package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"

	"github.com/piper/piper/pkg/notebook"
)

type notebookRepo struct{ dbstore.BaseRepo }

func NewNotebookRepo(exec *dbstore.Executor, source string) notebook.Repository {
	return &notebookRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

const notebookCols = `project_id, name, status, env, endpoint, pid, work_dir, token, worker_id, volume_id, image, yaml, created_at, updated_at`

func (r *notebookRepo) Create(ctx context.Context, nb *notebook.NotebookServer) error {
	now := time.Now()
	nb.CreatedAt = now
	nb.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO notebook_servers (project_id, name, status, env, endpoint, pid, work_dir, token, worker_id, volume_id, image, yaml, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			nb.ProjectID, nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.WorkerID, nb.VolumeID, nb.Image, nb.YAML,
			nb.CreatedAt, nb.UpdatedAt)
		return err
	})
}

func (r *notebookRepo) Get(ctx context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &nb,
			`SELECT `+notebookCols+` FROM notebook_servers WHERE project_id=? AND name=?`, projectID, name)
	})
	if err != nil {
		return nil, err
	}
	return &nb, nil
}

func (r *notebookRepo) Update(ctx context.Context, nb *notebook.NotebookServer) error {
	nb.UpdatedAt = time.Now()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE notebook_servers SET status=?, env=?, endpoint=?, pid=?, work_dir=?, token=?, worker_id=?, volume_id=?, image=?, yaml=?, updated_at=? WHERE project_id=? AND name=?`,
			nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.WorkerID, nb.VolumeID, nb.Image, nb.YAML, nb.UpdatedAt, nb.ProjectID, nb.Name)
		return err
	})
}

func (r *notebookRepo) SetStatus(ctx context.Context, projectID, name, status string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE notebook_servers SET status=?, updated_at=? WHERE project_id=? AND name=?`, status, time.Now(), projectID, name)
		return err
	})
}

func (r *notebookRepo) List(ctx context.Context, projectID string) ([]*notebook.NotebookServer, error) {
	var out []*notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT `+notebookCols+` FROM notebook_servers WHERE project_id=? ORDER BY created_at DESC`, projectID)
	})
	if out == nil {
		out = []*notebook.NotebookServer{}
	}
	return out, err
}

func (r *notebookRepo) ListByWorker(ctx context.Context, workerID string) ([]*notebook.NotebookServer, error) {
	var out []*notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT `+notebookCols+` FROM notebook_servers WHERE worker_id=? ORDER BY created_at DESC`, workerID)
	})
	return out, err
}

func (r *notebookRepo) GetByVolumeID(ctx context.Context, projectID, volumeID string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &nb,
			`SELECT `+notebookCols+` FROM notebook_servers WHERE project_id=? AND volume_id=? ORDER BY updated_at DESC LIMIT 1`, projectID, volumeID)
	})
	if err != nil {
		return nil, err
	}
	return &nb, nil
}

func (r *notebookRepo) Delete(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM notebook_servers WHERE project_id=? AND name=?`, projectID, name)
		return err
	})
}
