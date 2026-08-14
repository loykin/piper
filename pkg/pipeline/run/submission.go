package run

import (
	"context"
	"time"
)

// Submission is the Member-owned durable idempotency record for a Run
// creation request. RequestHash prevents a key from being reused for a
// different payload.
type Submission struct {
	ProjectID   string    `db:"project_id"`
	Key         string    `db:"idempotency_key"`
	RequestHash string    `db:"request_hash"`
	RunID       string    `db:"run_id"`
	CreatedAt   time.Time `db:"created_at"`
}

type SubmissionRepository interface {
	// Claim inserts value when the key is new. When it already exists,
	// claimed is false and existing contains the authoritative record.
	Claim(ctx context.Context, value *Submission) (existing *Submission, claimed bool, err error)
	Delete(ctx context.Context, projectID, key string) error
}
