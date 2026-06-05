package pipelinetemplate

import (
	"context"
	"time"
)

// Template is a versioned, named pipeline definition backed by an S3 snapshot.
// Every submit creates a new row; the submit history IS the version history.
type Template struct {
	ID         string    `json:"id"          db:"id"`
	Name       string    `json:"name"        db:"name"`
	YAML       string    `json:"yaml"        db:"yaml"`
	SnapshotID string    `json:"snapshot_id" db:"snapshot_id"`
	VolumeID   string    `json:"volume_id"   db:"volume_id"`
	CreatedAt  time.Time `json:"created_at"  db:"created_at"`
}

// Filter constrains a List query.
type Filter struct {
	Name  string // exact match; empty = all
	Limit int    // 0 = default (50)
}

// Repository is the persistence interface for Template records.
type Repository interface {
	Create(ctx context.Context, t *Template) error
	Get(ctx context.Context, id string) (*Template, error)
	List(ctx context.Context, f Filter) ([]*Template, error)
	Delete(ctx context.Context, id string) error
}
