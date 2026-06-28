package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/template"
)

type pipelineRepo struct{ dbstore.BaseRepo }

func NewPipelineRepo(exec *dbstore.Executor, source string) template.Repository {
	return &pipelineRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

const selectCols = `project_id, id, name, version, description, tags, yaml, snapshot_id, volume_id, created_at, updated_at`

func (r *pipelineRepo) NextVersion(ctx context.Context, projectID, name string) (int, error) {
	var maxVer int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COALESCE(MAX(version), 0) FROM pipeline_templates WHERE project_id=? AND name=?`)
		return db.GetContext(ctx, &maxVer, q, projectID, name)
	})
	return maxVer + 1, err
}

// Create inserts a new Template. t.Version must be set by the caller.
// Returns ErrVersionExists on (project_id, name, version) conflict.
func (r *pipelineRepo) Create(ctx context.Context, t *template.Template) error {
	if t.Tags == nil {
		t.Tags = []string{}
	}
	t.MarshalTagsJSON()

	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now

	if t.ID == "" {
		t.ID = uuid.NewString()
	}

	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`
			INSERT INTO pipeline_templates
			    (project_id, id, name, version, description, tags, yaml, snapshot_id, volume_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q,
			t.ProjectID, t.ID, t.Name, t.Version,
			t.Description, t.TagsJSON, t.YAML, t.SnapshotID, t.VolumeID,
			t.CreatedAt, t.UpdatedAt)
		if err != nil {
			if pgErr, ok := err.(*pq.Error); ok && pgErr.Code == "23505" {
				return template.ErrVersionExists
			}
		}
		return err
	})
}

func (r *pipelineRepo) Get(ctx context.Context, projectID, id string) (*template.Template, error) {
	var t template.Template
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + selectCols + ` FROM pipeline_templates WHERE project_id=? AND id=?`)
		return db.GetContext(ctx, &t, q, projectID, id)
	})
	if err != nil {
		return nil, err
	}
	t.AfterScan()
	return &t, nil
}

func (r *pipelineRepo) List(ctx context.Context, projectID string, f template.Filter) ([]*template.Template, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	var rows []*template.Template
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		if f.Name != "" {
			q := db.Rebind(`SELECT ` + selectCols + ` FROM pipeline_templates
				  WHERE project_id=? AND name=?
				  ORDER BY version DESC, created_at DESC LIMIT ?`)
			return db.SelectContext(ctx, &rows, q, projectID, f.Name, limit)
		}
		q := db.Rebind(`SELECT ` + selectCols + ` FROM pipeline_templates
			  WHERE project_id=?
			  ORDER BY created_at DESC LIMIT ?`)
		return db.SelectContext(ctx, &rows, q, projectID, limit)
	})
	if err != nil {
		return nil, err
	}
	for _, t := range rows {
		t.AfterScan()
	}
	return rows, nil
}

func (r *pipelineRepo) Delete(ctx context.Context, projectID, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM pipeline_templates WHERE project_id=? AND id=?`)
		_, err := db.ExecContext(ctx, q, projectID, id)
		return err
	})
}
