package alerting

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("alert rule not found")
	ErrAlreadyExists = errors.New("alert rule already exists")
	ErrInvalid       = errors.New("invalid alert rule")
)

type Repository interface {
	Create(context.Context, *Rule) error
	Get(ctx context.Context, projectID, id string) (*Rule, error)
	List(ctx context.Context, projectID string, limit, offset int) ([]*Rule, error)
	Count(ctx context.Context, projectID string) (int, error)
	ListEnabled(context.Context) ([]*Rule, error)
	Update(context.Context, *Rule) error
	Delete(ctx context.Context, projectID, id string) error
	TryClaimFire(ctx context.Context, projectID, id string, now, cooldownBefore time.Time) (bool, error)
	RecordDelivery(ctx context.Context, projectID, id string, at time.Time, success bool, message string) error
}
