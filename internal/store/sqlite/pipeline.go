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
		`INSERT INTO pipelines (project_id, id, name, yaml, snapshot_id, volume_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.ID, t.Name, t.YAML, t.SnapshotID, t.VolumeID, t.CreatedAt,
	)
	return err
}

func (r *pipelineRepo) Get(ctx context.Context, projectID, id string) (*template.Template, error) {
	var t template.Template
	err := r.db.GetContext(ctx, &t,
		`SELECT project_id, id, name, yaml, snapshot_id, volume_id, created_at FROM pipelines WHERE project_id=? AND id=?`, projectID, id)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *pipelineRepo) List(ctx context.Context, projectID string, f template.Filter) ([]*template.Template, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	var rows []*template.Template
	var err error
	if f.Name != "" {
		err = r.db.SelectContext(ctx, &rows,
			`SELECT project_id, id, name, yaml, snapshot_id, volume_id, created_at
			   FROM pipelines WHERE project_id=? AND name=? ORDER BY created_at DESC LIMIT ?`,
			projectID, f.Name, limit)
	} else {
		err = r.db.SelectContext(ctx, &rows,
			`SELECT project_id, id, name, yaml, snapshot_id, volume_id, created_at
			   FROM pipelines WHERE project_id=? ORDER BY created_at DESC LIMIT ?`,
			projectID, limit)
	}
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *pipelineRepo) Delete(ctx context.Context, projectID, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM pipelines WHERE project_id=? AND id=?`, projectID, id)
	return err
}
