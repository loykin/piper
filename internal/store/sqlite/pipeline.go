package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/template"
)

type pipelineRepo struct{ db *sqlx.DB }

// NewPipelineRepo creates a SQLite-backed template.Repository.
func NewPipelineRepo(db *sqlx.DB) template.Repository {
	return &pipelineRepo{db: db}
}

const selectCols = `project_id, id, name, version, description, tags, yaml, snapshot_id, volume_id, created_at, updated_at`

func (r *pipelineRepo) NextVersion(ctx context.Context, projectID, name string) (int, error) {
	var maxVer int
	err := r.db.GetContext(ctx, &maxVer,
		`SELECT COALESCE(MAX(version), 0) FROM pipeline_templates WHERE project_id=? AND name=?`,
		projectID, name)
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

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO pipeline_templates
		    (project_id, id, name, version, description, tags, yaml, snapshot_id, volume_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.ID, t.Name, t.Version,
		t.Description, t.TagsJSON, t.YAML, t.SnapshotID, t.VolumeID,
		t.CreatedAt, t.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return template.ErrVersionExists
	}
	return err
}

func (r *pipelineRepo) Get(ctx context.Context, projectID, id string) (*template.Template, error) {
	var t template.Template
	err := r.db.GetContext(ctx, &t,
		`SELECT `+selectCols+` FROM pipeline_templates WHERE project_id=? AND id=?`,
		projectID, id)
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
	var err error

	if f.Name != "" {
		err = r.db.SelectContext(ctx, &rows,
			`SELECT `+selectCols+` FROM pipeline_templates
			  WHERE project_id=? AND name=?
			  ORDER BY version DESC, created_at DESC LIMIT ?`,
			projectID, f.Name, limit)
	} else {
		err = r.db.SelectContext(ctx, &rows,
			`SELECT `+selectCols+` FROM pipeline_templates
			  WHERE project_id=?
			  ORDER BY created_at DESC LIMIT ?`,
			projectID, limit)
	}
	if err != nil {
		return nil, err
	}
	for _, t := range rows {
		t.AfterScan()
	}
	return rows, nil
}

func (r *pipelineRepo) Delete(ctx context.Context, projectID, id string) error {
	// Delete the specific version. Snapshot cleanup is the caller's responsibility.
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM pipeline_templates WHERE project_id=? AND id=?`, projectID, id)
	return err
}
