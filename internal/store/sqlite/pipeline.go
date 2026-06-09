package sqlite

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/template"
)

type pipelineRepo struct{ db *sqlx.DB }

// NewPipelineRepo creates a SQLite-backed template.Repository.
func NewPipelineRepo(db *sqlx.DB) template.Repository {
	return &pipelineRepo{db: db}
}

func (r *pipelineRepo) Create(ctx context.Context, t *template.Template) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO pipelines (id, name, yaml, snapshot_id, volume_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.YAML, t.SnapshotID, t.VolumeID, t.CreatedAt,
	)
	return err
}

func (r *pipelineRepo) Get(ctx context.Context, id string) (*template.Template, error) {
	var t template.Template
	err := r.db.GetContext(ctx, &t,
		`SELECT id, name, yaml, snapshot_id, volume_id, created_at FROM pipelines WHERE id=?`, id)
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
		err = r.db.SelectContext(ctx, &rows,
			`SELECT id, name, yaml, snapshot_id, volume_id, created_at
			   FROM pipelines WHERE name=? ORDER BY created_at DESC LIMIT ?`,
			f.Name, limit)
	} else {
		err = r.db.SelectContext(ctx, &rows,
			`SELECT id, name, yaml, snapshot_id, volume_id, created_at
			   FROM pipelines ORDER BY created_at DESC LIMIT ?`,
			limit)
	}
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *pipelineRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM pipelines WHERE id=?`, id)
	return err
}
