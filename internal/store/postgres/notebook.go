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
		q := db.Rebind(
			`INSERT INTO notebook_servers (project_id, name, status, env, endpoint, pid, work_dir, token, runtime_id, volume_id, image, yaml, created_by, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q,
			nb.ProjectID, nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.RuntimeID, nb.VolumeID, nb.Image, nb.YAML,
			nb.CreatedBy, nb.CreatedAt, nb.UpdatedAt)
		return err
	})
}

func (r *notebookRepo) Get(ctx context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + notebookCols + ` FROM notebook_servers WHERE project_id=? AND name=?`)
		return db.GetContext(ctx, &nb, q, projectID, name)
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
		q := db.Rebind(
			`UPDATE notebook_servers SET status=?, env=?, endpoint=?, pid=?, work_dir=?, token=?, runtime_id=?, volume_id=?, image=?, yaml=?, updated_at=? WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q,
			nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.RuntimeID, nb.VolumeID, nb.Image, nb.YAML, nb.UpdatedAt, nb.ProjectID, nb.Name)
		return err
	})
}

func (r *notebookRepo) SetStatus(ctx context.Context, projectID, name, status string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE notebook_servers SET status=?, updated_at=? WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q, status, time.Now(), projectID, name)
		return err
	})
}

func (r *notebookRepo) List(ctx context.Context, projectID string) ([]*notebook.NotebookServer, error) {
	var out []*notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + notebookCols + ` FROM notebook_servers WHERE project_id=? ORDER BY created_at DESC`)
		return db.SelectContext(ctx, &out, q, projectID)
	})
	if out == nil {
		out = []*notebook.NotebookServer{}
	}
	return out, err
}

func (r *notebookRepo) GetByVolumeID(ctx context.Context, projectID, volumeID string) (*notebook.NotebookServer, error) {
	var nb notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + notebookCols + ` FROM notebook_servers WHERE project_id=? AND volume_id=? ORDER BY updated_at DESC LIMIT 1`)
		return db.GetContext(ctx, &nb, q, projectID, volumeID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &nb, nil
}

// Delete archives the current row into notebook_history and removes it from
// notebook_servers in a single transaction. Both writes must succeed
// together — a history-insert failure used to be silently swallowed while
// the live row was deleted anyway, permanently losing the notebook's
// lifecycle record with no trace and no error surfaced to the caller.
func (r *notebookRepo) Delete(ctx context.Context, projectID, name string) error {
	return r.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		var nb notebook.NotebookServer
		q := tx.Rebind(`SELECT ` + notebookCols + ` FROM notebook_servers WHERE project_id=? AND name=?`)
		err := tx.GetContext(ctx, &nb, q, projectID, name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Nothing to archive; still remove the (already-absent) row below
			// so Delete stays idempotent, matching the prior behavior.
		case err != nil:
			return err
		default:
			if err := appendNotebookHistory(ctx, tx, &nb); err != nil {
				return fmt.Errorf("append history: %w", err)
			}
		}
		q = tx.Rebind(`DELETE FROM notebook_servers WHERE project_id=? AND name=?`)
		_, err = tx.ExecContext(ctx, q, projectID, name)
		return err
	})
}

// appendNotebookHistory is shared by AppendHistory (its own transaction) and
// Delete (inside Delete's transaction) so both go through identical SQL.
// sqlx.ExtContext is satisfied by both *sqlx.DB and *sqlx.Tx.
func appendNotebookHistory(ctx context.Context, execer sqlx.ExtContext, nb *notebook.NotebookServer) error {
	q := execer.Rebind(
		`INSERT INTO notebook_history (project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, image, yaml, created_by, deployed_at, stopped_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := execer.ExecContext(ctx, q,
		nb.ProjectID, nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.RuntimeID, nb.VolumeID, nb.Image, nb.YAML, nb.CreatedBy, nb.CreatedAt, time.Now())
	return err
}

func (r *notebookRepo) AppendHistory(ctx context.Context, nb *notebook.NotebookServer) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return appendNotebookHistory(ctx, db, nb)
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
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*notebook.NotebookHistory{}
	}
	return out, err
}

func (r *notebookRepo) CountHistory(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COUNT(*) FROM notebook_history WHERE project_id=?`)
		return db.GetContext(ctx, &count, q, projectID)
	})
	return count, err
}
