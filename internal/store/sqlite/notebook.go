package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"

	"github.com/loykin/piper/pkg/notebook"
)

type notebookRepo struct{ sqlxadapter.Source }

func NewNotebookRepo(exec *dbstore.Executor[*sqlx.DB], source string) notebook.Repository {
	return &notebookRepo{Source: sqlxadapter.NewSource(source, exec)}
}

const notebookCols = `project_id, name, status, env, endpoint, pid, work_dir, token, runtime_id, volume_id, image, yaml, created_by, created_at, updated_at`

func (r *notebookRepo) Create(ctx context.Context, nb *notebook.NotebookServer) error {
	now := time.Now()
	nb.CreatedAt = now
	nb.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO notebook_servers (project_id, name, status, env, endpoint, pid, work_dir, token, runtime_id, volume_id, image, yaml, created_by, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			nb.ProjectID, nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.RuntimeID, nb.VolumeID, nb.Image, nb.YAML,
			nb.CreatedBy, nb.CreatedAt, nb.UpdatedAt)
		return err
	})
}

func (r *notebookRepo) Get(ctx context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &nb,
			`SELECT `+notebookCols+` FROM notebook_servers WHERE project_id=? AND name=?`, projectID, name)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &nb, nil
}

func (r *notebookRepo) Update(ctx context.Context, nb *notebook.NotebookServer) error {
	nb.UpdatedAt = time.Now()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE notebook_servers SET status=?, env=?, endpoint=?, pid=?, work_dir=?, token=?, runtime_id=?, volume_id=?, image=?, yaml=?, updated_at=? WHERE project_id=? AND name=?`,
			nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.RuntimeID, nb.VolumeID, nb.Image, nb.YAML, nb.UpdatedAt, nb.ProjectID, nb.Name)
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

func (r *notebookRepo) GetByVolumeID(ctx context.Context, projectID, volumeID string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &nb,
			`SELECT `+notebookCols+` FROM notebook_servers WHERE project_id=? AND volume_id=? ORDER BY updated_at DESC LIMIT 1`, projectID, volumeID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &nb, nil
}

func (r *notebookRepo) Delete(ctx context.Context, projectID, name string) error {
	var nb notebook.NotebookServer
	if err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &nb,
			`SELECT `+notebookCols+` FROM notebook_servers WHERE project_id=? AND name=?`, projectID, name)
	}); err == nil {
		_ = r.AppendHistory(ctx, &nb)
	}
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM notebook_servers WHERE project_id=? AND name=?`, projectID, name)
		return err
	})
}

func (r *notebookRepo) AppendHistory(ctx context.Context, nb *notebook.NotebookServer) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO notebook_history (project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, image, yaml, created_by, deployed_at, stopped_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			nb.ProjectID, nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.RuntimeID, nb.VolumeID, nb.Image, nb.YAML, nb.CreatedBy, nb.CreatedAt, time.Now())
		return err
	})
}

func (r *notebookRepo) ListHistory(ctx context.Context, projectID string, limit, offset int) ([]*notebook.NotebookHistory, error) {
	query := `SELECT id, project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, image, yaml, created_by, deployed_at, stopped_at
		 FROM notebook_history WHERE project_id=? ORDER BY stopped_at DESC`
	args := []any{projectID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var out []*notebook.NotebookHistory
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, query, args...)
	})
	if out == nil {
		out = []*notebook.NotebookHistory{}
	}
	return out, err
}

func (r *notebookRepo) CountHistory(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &count, `SELECT COUNT(*) FROM notebook_history WHERE project_id=?`, projectID)
	})
	return count, err
}
