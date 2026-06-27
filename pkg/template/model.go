package template

import (
	"context"
	"encoding/json"
	"time"
)

// Template is a versioned, named pipeline definition backed by an S3 snapshot.
// Every submit creates a new row; the submit history IS the version history.
type Template struct {
	ProjectID   string    `json:"project_id"   db:"project_id"`
	TemplateID  string    `json:"template_id"  db:"template_id"`
	ID          string    `json:"id"           db:"id"` // version id
	Name        string    `json:"name"         db:"name"`
	Description string    `json:"description"  db:"description"`
	Tags        []string  `json:"tags"         db:"-"`
	TagsJSON    string    `json:"-"            db:"tags"`
	Version     int       `json:"version"      db:"version"`
	YAML        string    `json:"yaml"         db:"yaml"`
	SnapshotID  string    `json:"snapshot_id"  db:"snapshot_id"`
	VolumeID    string    `json:"volume_id"    db:"volume_id"`
	CreatedAt   time.Time `json:"created_at"   db:"created_at"`
}

// AfterScan populates Tags from the raw TagsJSON string after a DB scan.
func (t *Template) AfterScan() {
	if t.TagsJSON == "" || t.TagsJSON == "null" {
		t.Tags = []string{}
		return
	}
	_ = json.Unmarshal([]byte(t.TagsJSON), &t.Tags)
	if t.Tags == nil {
		t.Tags = []string{}
	}
}

// MarshalTagsJSON encodes Tags into TagsJSON before a DB write.
func (t *Template) MarshalTagsJSON() {
	b, _ := json.Marshal(t.Tags)
	t.TagsJSON = string(b)
}

// Filter constrains a List query.
type Filter struct {
	Name       string // exact match; empty = all
	TemplateID string // exact match; empty = all
	Limit      int    // 0 = default (50)
}

// Repository is the persistence interface for Template records.
type Repository interface {
	Create(ctx context.Context, t *Template) error
	Get(ctx context.Context, projectID, id string) (*Template, error)
	List(ctx context.Context, projectID string, f Filter) ([]*Template, error)
	Delete(ctx context.Context, projectID, id string) error
	// UpdateMeta updates description and tags on the template group (not a single version).
	UpdateMeta(ctx context.Context, projectID, templateID, description string, tags []string) error
}
