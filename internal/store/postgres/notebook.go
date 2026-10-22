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

const notebookCols = `project_id, name, status, env, endpoint, pid, work_dir, token, runtime_id, volume_id, reason, yaml, created_by, created_at, started_at, updated_at`

func (r *notebookRepo) Create(ctx context.Context, nb *notebook.NotebookServer) error {
	now := time.Now()
	nb.CreatedAt = now
	nb.StartedAt = now
	nb.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(
			`INSERT INTO notebook_servers (project_id, name, status, env, endpoint, pid, work_dir, token, runtime_id, volume_id, reason, yaml, created_by, created_at, started_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q,
			nb.ProjectID, nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.RuntimeID, nb.VolumeID, nb.Reason, nb.YAML,
			nb.CreatedBy, nb.CreatedAt, nb.StartedAt, nb.UpdatedAt)
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
			`UPDATE notebook_servers SET status=?, env=?, endpoint=?, pid=?, work_dir=?, token=?, runtime_id=?, volume_id=?, reason=?, yaml=?, updated_at=? WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q,
			nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.Token, nb.RuntimeID, nb.VolumeID, nb.Reason, nb.YAML, nb.UpdatedAt, nb.ProjectID, nb.Name)
		return err
	})
}

func (r *notebookRepo) SetStatus(ctx context.Context, projectID, name, status, reason string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE notebook_servers SET status=?, reason=?, updated_at=? WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q, status, reason, time.Now(), projectID, name)
		return err
	})
}

func (r *notebookRepo) List(ctx context.Context, projectID string, limit, offset int) ([]*notebook.NotebookServer, error) {
	query := `SELECT ` + notebookCols + ` FROM notebook_servers WHERE project_id=? ORDER BY created_at DESC`
	args := []any{projectID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var out []*notebook.NotebookServer
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*notebook.NotebookServer{}
	}
	return out, err
}

func (r *notebookRepo) Count(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &count, db.Rebind(`SELECT COUNT(*) FROM notebook_servers WHERE project_id=?`), projectID)
	})
	return count, err
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

// Delete removes the notebook row. History is not written here: the
// notebook's last run was recorded when it ended (FinishRun).
func (r *notebookRepo) Delete(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM notebook_servers WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q, projectID, name)
		return err
	})
}

func (r *notebookRepo) StartRun(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		now := time.Now()
		q := db.Rebind(`UPDATE notebook_servers SET status=?, reason='', started_at=?, updated_at=? WHERE project_id=? AND name=?`)
		_, err := db.ExecContext(ctx, q, notebook.StatusStarting, now, now, projectID, name)
		return err
	})
}

// FinishRun ends the current run and records it in notebook_history in one
// transaction. The status guard makes the transition happen once: a second
// report of the same end (Delete's stop racing the driver's) updates no row
// and records nothing.
func (r *notebookRepo) FinishRun(ctx context.Context, projectID, name, status, reason string) (bool, error) {
	finished := false
	err := r.RunTx(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		now := time.Now()
		q := tx.Rebind(`UPDATE notebook_servers SET status=?, reason=?, endpoint='', pid=0, token='', updated_at=?
			WHERE project_id=? AND name=? AND status NOT IN (?, ?)`)
		res, err := tx.ExecContext(ctx, q, status, reason, now, projectID, name, notebook.StatusStopped, notebook.StatusFailed)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		var nb notebook.NotebookServer
		q = tx.Rebind(`SELECT ` + notebookCols + ` FROM notebook_servers WHERE project_id=? AND name=?`)
		if err := tx.GetContext(ctx, &nb, q, projectID, name); err != nil {
			return err
		}
		// One row per run: a run the runtime revived on its own (K8s
		// restarting a crashed container) and that ended again updates its
		// row to the latest end instead of adding another.
		q = tx.Rebind(`UPDATE notebook_history SET status=?, reason=?, stopped_at=?
			WHERE project_id=? AND name=? AND deployed_at=?`)
		res, err = tx.ExecContext(ctx, q, nb.Status, nb.Reason, now, projectID, name, nb.StartedAt)
		if err != nil {
			return fmt.Errorf("update history: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			q = tx.Rebind(
				`INSERT INTO notebook_history (project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, reason, yaml, created_by, deployed_at, stopped_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
			if _, err := tx.ExecContext(ctx, q,
				nb.ProjectID, nb.Name, nb.Status, nb.Env, nb.Endpoint, nb.PID, nb.WorkDir, nb.RuntimeID, nb.VolumeID, nb.Reason, nb.YAML, nb.CreatedBy, nb.StartedAt, now); err != nil {
				return fmt.Errorf("append history: %w", err)
			}
		}
		finished = true
		return nil
	})
	if err != nil {
		finished = false
	}
	return finished, err
}

func (r *notebookRepo) ListHistory(ctx context.Context, projectID string, limit, offset int) ([]*notebook.NotebookHistory, error) {
	query := `SELECT id, project_id, name, status, env, endpoint, pid, work_dir, runtime_id, volume_id, reason, yaml, created_by, deployed_at, stopped_at
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

func (r *notebookRepo) PurgeHistoryBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM notebook_history WHERE stopped_at < ?`)
		res, err := db.ExecContext(ctx, q, cutoff)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	return affected, err
}
