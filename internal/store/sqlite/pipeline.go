package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/template"
)

type pipelineRepo struct{ sqlxadapter.Source }

func NewPipelineRepo(exec *dbstore.Executor[*sqlx.DB], source string) template.Repository {
	return &pipelineRepo{Source: sqlxadapter.NewSource(source, exec)}
}

const selectCols = `project_id, id, name, version, description, tags, yaml, snapshot_id, volume_id, created_at, updated_at, storage_backend`

func (r *pipelineRepo) NextVersion(ctx context.Context, projectID, name string) (int, error) {
	var maxVer int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &maxVer,
			`SELECT COALESCE(MAX(version), 0) FROM pipeline_templates WHERE project_id=? AND name=?`,
			projectID, name)
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
		_, err := db.ExecContext(ctx, `
			INSERT INTO pipeline_templates
			    (project_id, id, name, version, description, tags, yaml, snapshot_id, volume_id, created_at, updated_at, storage_backend)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ProjectID, t.ID, t.Name, t.Version,
			t.Description, t.TagsJSON, t.YAML, t.SnapshotID, t.VolumeID,
			t.CreatedAt, t.UpdatedAt, t.StorageBackend)
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return template.ErrVersionExists
		}
		return err
	})
}

func (r *pipelineRepo) Get(ctx context.Context, projectID, id string) (*template.Template, error) {
	var t template.Template
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &t,
			`SELECT `+selectCols+` FROM pipeline_templates WHERE project_id=? AND id=?`,
			projectID, id)
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
			return db.SelectContext(ctx, &rows,
				`SELECT `+selectCols+` FROM pipeline_templates
				  WHERE project_id=? AND name=?
				  ORDER BY version DESC, created_at DESC LIMIT ? OFFSET ?`,
				projectID, f.Name, limit, f.Offset)
		}
		return db.SelectContext(ctx, &rows,
			`SELECT `+selectCols+` FROM pipeline_templates
			  WHERE project_id=?
			  ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			projectID, limit, f.Offset)
	})
	if err != nil {
		return nil, err
	}
	for _, t := range rows {
		t.AfterScan()
	}
	return rows, nil
}

func (r *pipelineRepo) Count(ctx context.Context, projectID string, f template.Filter) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		if f.Name != "" {
			return db.GetContext(ctx, &count,
				`SELECT COUNT(*) FROM pipeline_templates WHERE project_id=? AND name=?`, projectID, f.Name)
		}
		return db.GetContext(ctx, &count,
			`SELECT COUNT(*) FROM pipeline_templates WHERE project_id=?`, projectID)
	})
	return count, err
}

func (r *pipelineRepo) Delete(ctx context.Context, projectID, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`DELETE FROM pipeline_templates WHERE project_id=? AND id=?`, projectID, id)
		return err
	})
}
