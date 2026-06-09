package postgres

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/template"
)

type pipelineRepo struct{ db *sqlx.DB }

// NewPipelineRepo creates a PostgreSQL-backed template.Repository.
func NewPipelineRepo(db *sqlx.DB) template.Repository {
	return &pipelineRepo{db: db}
}

func (r *pipelineRepo) Create(ctx context.Context, t *template.Template) error {
	q := r.db.Rebind(`INSERT INTO pipelines (id, name, yaml, snapshot_id, volume_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, q, t.ID, t.Name, t.YAML, t.SnapshotID, t.VolumeID, t.CreatedAt)
	return err
}

func (r *pipelineRepo) Get(ctx context.Context, id string) (*template.Template, error) {
	var t template.Template
	q := r.db.Rebind(`SELECT id, name, yaml, snapshot_id, volume_id, created_at FROM pipelines WHERE id=?`)
	err := r.db.GetContext(ctx, &t, q, id)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *pipelineRepo) List(ctx context.Context, f template.Filter) ([]*template.Template, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	var rows []*template.Template
	var err error
	if f.Name != "" {
		q := r.db.Rebind(`SELECT id, name, yaml, snapshot_id, volume_id, created_at
			   FROM pipelines WHERE name=? ORDER BY created_at DESC LIMIT ?`)
		err = r.db.SelectContext(ctx, &rows, q, f.Name, limit)
	} else {
		q := r.db.Rebind(`SELECT id, name, yaml, snapshot_id, volume_id, created_at
			   FROM pipelines ORDER BY created_at DESC LIMIT ?`)
		err = r.db.SelectContext(ctx, &rows, q, limit)
	}
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *pipelineRepo) Delete(ctx context.Context, id string) error {
	q := r.db.Rebind(`DELETE FROM pipelines WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, id)
	return err
}
