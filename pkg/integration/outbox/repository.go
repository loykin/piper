package outbox

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when an operation targets an event ID that
	// does not exist (or no longer matches the expected state, e.g. a
	// Mark* call racing a lease reclaim).
	ErrNotFound = errors.New("outbox: event not found")
)

// Repository is the persistence interface for the durable integration
// outbox (design doc section 6.3). Implementations must support both
// SQLite and Postgres with the same semantics; SQLite claims are expected
// to run with dispatcher concurrency 1 (design doc: "SQLite에서는 SKIP
// LOCKED를 흉내 내려고 복잡한 동시 worker를 만들지 않고 기본 dispatcher
// concurrency를 1로 둔다"), so SQLite implementations are not required to
// use a locking claim strategy — a plain SELECT-then-UPDATE under the
// existing single-writer dbstore executor is sufficient. Postgres
// implementations should use `FOR UPDATE SKIP LOCKED` to support bounded
// concurrent dispatchers.
type Repository interface {
	// Enqueue durably records a new event. If event.Sequence is 0, the
	// implementation assigns the next sequence for
	// (IntegrationID, AggregateType, AggregateID) atomically (current
	// max + 1, starting at 1) — callers normally leave it 0 and let the
	// repository own ordering. event.ID must already be set by the caller
	// (uuid), event.Status/Attempts/NextAttemptAt/CreatedAt are set by the
	// implementation (Status=StatusPending, Attempts=0, NextAttemptAt=now,
	// CreatedAt=now) regardless of what the caller passed in. Enqueue must
	// be fast (design doc section 4.3) — it performs no outbound calls, only
	// a local DB write.
	Enqueue(ctx context.Context, event *Event) error

	// ClaimBatch atomically claims up to limit events for owner: rows that
	// are StatusPending and due (NextAttemptAt <= now), or StatusDelivering
	// with an expired lease (LeaseExpiresAt < now — a dispatcher that died
	// mid-delivery). Ordering guarantee (design doc section 10.3): for a
	// given aggregate (AggregateType, AggregateID), only the
	// lowest-Sequence event that is not yet StatusDelivered/StatusDead/
	// StatusDisabled is claimable — a later event for the same aggregate is
	// never claimed while an earlier one is still pending/delivering. Claimed
	// rows are set to StatusDelivering, LeaseOwner=owner,
	// LeaseExpiresAt=now+leaseDuration, and Attempts is incremented (an
	// attempt is being made). Rows are returned ordered by NextAttemptAt
	// ascending. limit <= 0 returns no rows.
	ClaimBatch(ctx context.Context, owner string, leaseDuration time.Duration, limit int) ([]*Event, error)

	// MarkDelivered transitions a claimed (StatusDelivering, owned by
	// owner) event to StatusDelivered. Returns ErrNotFound if the event
	// does not exist or is not currently leased to owner (e.g. the lease
	// expired and another dispatcher already reclaimed it).
	MarkDelivered(ctx context.Context, id, owner string) error

	// MarkRetry transitions a claimed event back to StatusPending with
	// NextAttemptAt=nextAttemptAt and the given redacted error code/message
	// recorded (design doc section 15.2 — callers must never pass a raw
	// remote response body or credential here). Returns ErrNotFound under
	// the same conditions as MarkDelivered.
	MarkRetry(ctx context.Context, id, owner string, nextAttemptAt time.Time, errorCode, errorMessage string) error

	// MarkDead transitions a claimed event to the terminal StatusDead state
	// with the given redacted error code/message. Returns ErrNotFound under
	// the same conditions as MarkDelivered.
	MarkDead(ctx context.Context, id, owner string, errorCode, errorMessage string) error

	// DisableIntegrationEvents bulk-transitions every StatusPending/
	// StatusDelivering event for integrationID to StatusDisabled (design
	// doc section 11.1's integration-delete semantics: "dispatcher 중지,
	// pending outbox를 disabled 상태로 보존"). Returns the number of rows
	// transitioned. Idempotent — calling it again on an already-disabled
	// integration transitions zero additional rows.
	DisableIntegrationEvents(ctx context.Context, integrationID string) (int, error)

	// CountByStatus returns the number of events for integrationID in the
	// given status (observability: pending/dead backlog counts, design doc
	// section 11.1's GET /mlflow-integrations/{id} health+backlog).
	CountByStatus(ctx context.Context, integrationID, status string) (int, error)

	// OldestPending returns the CreatedAt of the oldest StatusPending event
	// for integrationID, or nil if there is none.
	OldestPending(ctx context.Context, integrationID string) (*time.Time, error)

	// ListByAggregate returns every event for
	// (integrationID, aggregateType, aggregateID) ordered by Sequence
	// ascending. Used by tests to verify ordering and by a future
	// reconciler/sync-job UI to show per-run delivery history.
	ListByAggregate(ctx context.Context, integrationID, aggregateType, aggregateID string) ([]*Event, error)
}
