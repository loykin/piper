package projectclient

import (
	"context"
	"time"
)

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
