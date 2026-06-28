package template

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrVersionExists is returned by Repository.Create when (project_id, name, version)
// already exists. Callers should map this to HTTP 409 Conflict.
var ErrVersionExists = errors.New("pipeline template version already exists")

// Template is a versioned pipeline definition.
// (project_id, name, version) is the natural unique key.
// id is a UUID kept for stable external references (schedule.template_version_id).
type Template struct {
	ID          string    `json:"id"          db:"id"`
	ProjectID   string    `json:"project_id"  db:"project_id"`
	Name        string    `json:"name"        db:"name"`
	Version     int       `json:"version"     db:"version"`
	Description string    `json:"description" db:"description"`
	Tags        []string  `json:"tags"        db:"-"`
	TagsJSON    string    `json:"-"           db:"tags"`
	YAML        string    `json:"yaml"        db:"yaml"`
	SnapshotID  string    `json:"snapshot_id" db:"snapshot_id"`
	VolumeID    string    `json:"volume_id"   db:"volume_id"`
	CreatedAt   time.Time `json:"created_at"  db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"  db:"updated_at"`
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
	Name  string // filter by template name (group); empty = all
	Limit int    // 0 = default (50)
}

// Repository is the persistence interface for Template records.
type Repository interface {
	// NextVersion returns max(version)+1 for the given (project_id, name).
	// Returns 1 if no versions exist yet.
	NextVersion(ctx context.Context, projectID, name string) (int, error)
	// Create inserts a new Template. t.Version must be set by the caller.
	// Returns ErrVersionExists on (project_id, name, version) conflict.
	Create(ctx context.Context, t *Template) error
	Get(ctx context.Context, projectID, id string) (*Template, error)
	List(ctx context.Context, projectID string, f Filter) ([]*Template, error)
	Delete(ctx context.Context, projectID, id string) error
}
