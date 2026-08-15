package projectclient

import (
	"context"
	"time"
)

// StaleClaimWindow is how long an idempotency claim may sit incomplete
// (the process crashed, or the Complete write itself failed) before a
// retry carrying the same key and request body is allowed to reclaim it
// and re-run the mutation, instead of the key being stuck returning 409
// forever.
const StaleClaimWindow = 30 * time.Second

type Mutation struct {
	ProjectID   string    `db:"project_id"`
	Key         string    `db:"idempotency_key"`
	RequestHash string    `db:"request_hash"`
	Status      int       `db:"response_status"`
	HeaderJSON  []byte    `db:"response_headers"`
	Body        []byte    `db:"response_body"`
	Completed   bool      `db:"completed"`
	CreatedAt   time.Time `db:"created_at"`
}

type MutationRepository interface {
	Claim(context.Context, *Mutation) (*Mutation, bool, error)
	Complete(context.Context, *Mutation) error
}
