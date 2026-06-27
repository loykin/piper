package sqlite

import (
	"context"
	"database/sql"
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

const versionSelectCols = `v.project_id, v.template_id, v.id, t.name, t.description, t.tags,
	v.version, v.yaml, v.snapshot_id, v.volume_id, v.created_at`

func (r *pipelineRepo) Create(ctx context.Context, t *template.Template) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := t.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
		t.CreatedAt = now
	}

	if t.Tags == nil {
		t.Tags = []string{}
	}
	t.MarshalTagsJSON()

	var templateID string
	if t.TemplateID != "" {
		err = tx.GetContext(ctx, &templateID,
			`SELECT id FROM pipeline_templates WHERE project_id=? AND id=?`, t.ProjectID, t.TemplateID)
		if err != nil {
			return err
		}
		var templateName string
		if err := tx.GetContext(ctx, &templateName,
			`SELECT name FROM pipeline_templates WHERE project_id=? AND id=?`, t.ProjectID, templateID); err != nil {
			return err
		}
		t.Name = templateName
	} else {
		err = tx.GetContext(ctx, &templateID,
			`SELECT id FROM pipeline_templates WHERE project_id=? AND name=?`, t.ProjectID, t.Name)
		if err != nil {
			if err != sql.ErrNoRows {
				return err
			}
			templateID = uuid.NewString()
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO pipeline_templates (project_id, id, name, description, tags, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				t.ProjectID, templateID, t.Name, t.Description, t.TagsJSON, now, now); err != nil {
				return err
			}
		}
	}

	var version int
	if err := tx.GetContext(ctx, &version,
		`SELECT COALESCE(MAX(version), 0) + 1
		   FROM pipeline_template_versions
		  WHERE project_id=? AND template_id=?`, t.ProjectID, templateID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pipeline_template_versions (project_id, id, template_id, version, yaml, snapshot_id, volume_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.ID, templateID, version, t.YAML, t.SnapshotID, t.VolumeID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE pipeline_templates SET updated_at=? WHERE project_id=? AND id=?`,
		now, t.ProjectID, templateID); err != nil {
		return err
	}
	t.TemplateID = templateID
	t.Version = version
	return tx.Commit()
}

func (r *pipelineRepo) Get(ctx context.Context, projectID, id string) (*template.Template, error) {
	var t template.Template
	err := r.db.GetContext(ctx, &t,
		`SELECT `+versionSelectCols+`
		   FROM pipeline_template_versions v
		   JOIN pipeline_templates t ON t.project_id = v.project_id AND t.id = v.template_id
		  WHERE v.project_id=? AND v.id=?`, projectID, id)
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
	base := `SELECT ` + versionSelectCols + `
		   FROM pipeline_template_versions v
		   JOIN pipeline_templates t ON t.project_id = v.project_id AND t.id = v.template_id
		  WHERE v.project_id=?`

	if f.TemplateID != "" {
		err = r.db.SelectContext(ctx, &rows,
			base+` AND v.template_id=? ORDER BY v.created_at DESC LIMIT ?`,
			projectID, f.TemplateID, limit)
	} else if f.Name != "" {
		err = r.db.SelectContext(ctx, &rows,
			base+` AND t.name=? ORDER BY v.created_at DESC LIMIT ?`,
			projectID, f.Name, limit)
	} else {
		err = r.db.SelectContext(ctx, &rows,
			base+` ORDER BY v.created_at DESC LIMIT ?`,
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

func (r *pipelineRepo) UpdateMeta(ctx context.Context, projectID, templateID, description string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	t := &template.Template{Tags: tags}
	t.MarshalTagsJSON()
	_, err := r.db.ExecContext(ctx,
		`UPDATE pipeline_templates SET description=?, tags=?, updated_at=? WHERE project_id=? AND id=?`,
		description, t.TagsJSON, time.Now().UTC(), projectID, templateID)
	return err
}

func (r *pipelineRepo) Delete(ctx context.Context, projectID, id string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var templateID string
	err = tx.GetContext(ctx, &templateID,
		`SELECT template_id FROM pipeline_template_versions WHERE project_id=? AND id=?`, projectID, id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pipeline_template_versions WHERE project_id=? AND id=?`, projectID, id); err != nil {
		return err
	}
	if templateID != "" {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM pipeline_templates
			  WHERE project_id=? AND id=?
			    AND NOT EXISTS (
			      SELECT 1 FROM pipeline_template_versions
			       WHERE project_id=? AND template_id=?
			    )`,
			projectID, templateID, projectID, templateID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
