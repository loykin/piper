package postgres

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

type notebookVolumeRepo struct{ sqlxadapter.Source }

func NewNotebookVolumeRepo(exec *dbstore.Executor[*sqlx.DB], source string) notebook.VolumeRepository {
	return &notebookVolumeRepo{Source: sqlxadapter.NewSource(source, exec)}
}

const pgVolumeCols = `project_id, id, label, work_dir, status, runtime_id, created_at, updated_at`

func (r *notebookVolumeRepo) Create(ctx context.Context, v *notebook.NotebookVolume) error {
	now := time.Now()
	v.CreatedAt = now
	v.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(
			`INSERT INTO notebook_volumes (project_id, id, label, work_dir, status, runtime_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q,
			v.ProjectID, v.ID, v.Label, v.WorkDir, v.Status, v.RuntimeID, v.CreatedAt, v.UpdatedAt)
		return err
	})
}

func (r *notebookVolumeRepo) Get(ctx context.Context, id string) (*notebook.NotebookVolume, error) {
	var v notebook.NotebookVolume
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + pgVolumeCols + ` FROM notebook_volumes WHERE id=?`)
		return db.GetContext(ctx, &v, q, id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *notebookVolumeRepo) List(ctx context.Context, projectID string, limit, offset int) ([]*notebook.NotebookVolume, error) {
	query := `SELECT ` + pgVolumeCols + ` FROM notebook_volumes WHERE project_id=? ORDER BY created_at DESC`
	args := []any{projectID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var out []*notebook.NotebookVolume
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*notebook.NotebookVolume{}
	}
	return out, err
}

func (r *notebookVolumeRepo) Count(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &count, db.Rebind(`SELECT COUNT(*) FROM notebook_volumes WHERE project_id=?`), projectID)
	})
	return count, err
}

func (r *notebookVolumeRepo) Update(ctx context.Context, v *notebook.NotebookVolume) error {
	v.UpdatedAt = time.Now()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE notebook_volumes SET label=?, work_dir=?, status=?, runtime_id=?, updated_at=? WHERE id=?`)
		_, err := db.ExecContext(ctx, q, v.Label, v.WorkDir, v.Status, v.RuntimeID, v.UpdatedAt, v.ID)
		return err
	})
}

func (r *notebookVolumeRepo) SetStatus(ctx context.Context, id, status string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE notebook_volumes SET status=?, updated_at=? WHERE id=?`)
		_, err := db.ExecContext(ctx, q, status, time.Now(), id)
		return err
	})
}

func (r *notebookVolumeRepo) Delete(ctx context.Context, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM notebook_volumes WHERE id=?`)
		_, err := db.ExecContext(ctx, q, id)
		return err
	})
}
