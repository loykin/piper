package outbox_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/pkg/integration/outbox"
)

// fakeRepo is a minimal in-memory outbox.Repository for dispatcher tests —
// no per-aggregate ordering gate (that's covered by the real SQLite/
// Postgres repository conformance tests in internal/store/repotest), just
// enough claim/lease/retry/dead-letter bookkeeping to exercise Dispatcher's
// decision logic in isolation.
type fakeRepo struct {
	mu     sync.Mutex
	events map[string]*outbox.Event
}

func newFakeRepo() *fakeRepo { return &fakeRepo{events: map[string]*outbox.Event{}} }

func (r *fakeRepo) Enqueue(ctx context.Context, e *outbox.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	e.Status = string(outbox.StatusPending)
	e.CreatedAt = time.Now().UTC()
	e.NextAttemptAt = e.CreatedAt
	r.events[e.ID] = e
	return nil
}

func (r *fakeRepo) ClaimBatch(ctx context.Context, owner string, leaseDuration time.Duration, limit int) ([]*outbox.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	var out []*outbox.Event
	for _, e := range r.events {
		if len(out) >= limit {
			break
		}
		due := e.Status == string(outbox.StatusPending) && !e.NextAttemptAt.After(now)
		expired := e.Status == string(outbox.StatusDelivering) && e.LeaseExpiresAt != nil && e.LeaseExpiresAt.Before(now)
		if !due && !expired {
			continue
		}
		e.Status = string(outbox.StatusDelivering)
		e.LeaseOwner = owner
		exp := now.Add(leaseDuration)
		e.LeaseExpiresAt = &exp
		e.Attempts++
		out = append(out, e)
	}
	return out, nil
}

func (r *fakeRepo) MarkDelivered(ctx context.Context, id, owner string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.events[id]
	if e == nil || e.LeaseOwner != owner {
		return outbox.ErrNotFound
	}
	e.Status = string(outbox.StatusDelivered)
	return nil
}

func (r *fakeRepo) MarkRetry(ctx context.Context, id, owner string, next time.Time, code, msg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.events[id]
	if e == nil || e.LeaseOwner != owner {
		return outbox.ErrNotFound
	}
	e.Status = string(outbox.StatusPending)
	e.NextAttemptAt = next
	e.LastErrorCode, e.LastError = code, msg
	return nil
}

func (r *fakeRepo) MarkDead(ctx context.Context, id, owner, code, msg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.events[id]
	if e == nil || e.LeaseOwner != owner {
		return outbox.ErrNotFound
	}
	e.Status = string(outbox.StatusDead)
	e.LastErrorCode, e.LastError = code, msg
	return nil
}

func (r *fakeRepo) DisableIntegrationEvents(ctx context.Context, integrationID string) (int, error) {
	return 0, nil
}
func (r *fakeRepo) CountByStatus(ctx context.Context, integrationID, status string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.IntegrationID == integrationID && e.Status == status {
			n++
		}
	}
	return n, nil
}
func (r *fakeRepo) OldestPending(ctx context.Context, integrationID string) (*time.Time, error) {
	return nil, nil
}
func (r *fakeRepo) Backlog(ctx context.Context, integrationIDs []string) (map[string]outbox.Backlog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]outbox.Backlog, len(integrationIDs))
	want := make(map[string]bool, len(integrationIDs))
	for _, id := range integrationIDs {
		want[id] = true
	}
	for _, e := range r.events {
		if !want[e.IntegrationID] {
			continue
		}
		b := out[e.IntegrationID]
		switch e.Status {
		case string(outbox.StatusPending):
			b.Pending++
		case string(outbox.StatusDead):
			b.Dead++
		}
		out[e.IntegrationID] = b
	}
	return out, nil
}
func (r *fakeRepo) ListByAggregate(ctx context.Context, integrationID, aggregateType, aggregateID string) ([]*outbox.Event, error) {
	return nil, nil
}

func (r *fakeRepo) get(id string) *outbox.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[id]
}

func TestDispatcher_DeliversSuccessfully(t *testing.T) {
	repo := newFakeRepo()
	ev := &outbox.Event{IntegrationID: "int-1", EventType: "pipeline_run.created"}
	if err := repo.Enqueue(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	d := outbox.NewDispatcher(repo, outbox.HandlerFunc(func(ctx context.Context, e *outbox.Event) outbox.Outcome {
		return outbox.Outcome{Delivered: true}
	}), outbox.Config{Owner: "test"})
	n := d.PollOnce(context.Background())
	if n != 1 {
		t.Fatalf("PollOnce claimed %d, want 1", n)
	}
	if got := repo.get(ev.ID).Status; got != string(outbox.StatusDelivered) {
		t.Fatalf("status = %q, want delivered", got)
	}
}

func TestDispatcher_RetryableFailureReschedulesWithBackoff(t *testing.T) {
	repo := newFakeRepo()
	ev := &outbox.Event{IntegrationID: "int-1", EventType: "pipeline_run.created"}
	if err := repo.Enqueue(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	d := outbox.NewDispatcher(repo, outbox.HandlerFunc(func(ctx context.Context, e *outbox.Event) outbox.Outcome {
		return outbox.Outcome{Retryable: true, ErrorCode: "HTTP_503", ErrorMessage: "unavailable"}
	}), outbox.Config{Owner: "test", BaseBackoff: 100 * time.Millisecond, MaxBackoff: time.Second, MaxAttemptsBeforeDead: 5})
	before := time.Now().UTC()
	d.PollOnce(context.Background())
	got := repo.get(ev.ID)
	if got.Status != string(outbox.StatusPending) {
		t.Fatalf("status = %q, want pending (rescheduled)", got.Status)
	}
	if !got.NextAttemptAt.After(before) {
		t.Fatalf("NextAttemptAt = %v, want after %v (backoff applied)", got.NextAttemptAt, before)
	}
	if got.LastErrorCode != "HTTP_503" {
		t.Fatalf("LastErrorCode = %q, want HTTP_503", got.LastErrorCode)
	}
}

func TestDispatcher_RetryAfterOverridesComputedBackoff(t *testing.T) {
	repo := newFakeRepo()
	ev := &outbox.Event{IntegrationID: "int-1", EventType: "pipeline_run.created"}
	if err := repo.Enqueue(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	d := outbox.NewDispatcher(repo, outbox.HandlerFunc(func(ctx context.Context, e *outbox.Event) outbox.Outcome {
		return outbox.Outcome{Retryable: true, RetryAfter: 10 * time.Minute}
	}), outbox.Config{Owner: "test", BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	before := time.Now().UTC()
	d.PollOnce(context.Background())
	got := repo.get(ev.ID)
	if got.NextAttemptAt.Before(before.Add(9 * time.Minute)) {
		t.Fatalf("NextAttemptAt = %v, want honoring the 10m Retry-After override, not the ~0 computed backoff", got.NextAttemptAt)
	}
}

func TestDispatcher_NonRetryableFailureGoesDeadImmediately(t *testing.T) {
	repo := newFakeRepo()
	ev := &outbox.Event{IntegrationID: "int-1", EventType: "pipeline_run.created"}
	if err := repo.Enqueue(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	d := outbox.NewDispatcher(repo, outbox.HandlerFunc(func(ctx context.Context, e *outbox.Event) outbox.Outcome {
		return outbox.Outcome{Retryable: false, ErrorCode: "HTTP_401", ErrorMessage: "unauthorized"}
	}), outbox.Config{Owner: "test", MaxAttemptsBeforeDead: 20})
	d.PollOnce(context.Background())
	got := repo.get(ev.ID)
	if got.Status != string(outbox.StatusDead) {
		t.Fatalf("status = %q, want dead (non-retryable error must not be retried)", got.Status)
	}
	if got.LastErrorCode != "HTTP_401" {
		t.Fatalf("LastErrorCode = %q, want HTTP_401", got.LastErrorCode)
	}
}

func TestDispatcher_RetryableFailureGoesDeadOnceAttemptsExhausted(t *testing.T) {
	repo := newFakeRepo()
	ev := &outbox.Event{IntegrationID: "int-1", EventType: "pipeline_run.created"}
	if err := repo.Enqueue(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	d := outbox.NewDispatcher(repo, outbox.HandlerFunc(func(ctx context.Context, e *outbox.Event) outbox.Outcome {
		return outbox.Outcome{Retryable: true, ErrorCode: "HTTP_503", RetryAfter: time.Microsecond}
	}), outbox.Config{Owner: "test", MaxAttemptsBeforeDead: 3, BaseBackoff: time.Microsecond, MaxBackoff: time.Microsecond})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		d.PollOnce(ctx)
		time.Sleep(2 * time.Millisecond)
	}
	got := repo.get(ev.ID)
	if got.Status != string(outbox.StatusDead) {
		t.Fatalf("after %d attempts (MaxAttemptsBeforeDead=3), status = %q, want dead", got.Attempts, got.Status)
	}
}

func TestDispatcher_ConcurrencyOneProcessesSequentially(t *testing.T) {
	repo := newFakeRepo()
	for i := 0; i < 5; i++ {
		if err := repo.Enqueue(context.Background(), &outbox.Event{IntegrationID: "int-1", EventType: "pipeline_run.created"}); err != nil {
			t.Fatal(err)
		}
	}
	var maxInFlight, inFlight int32Counter
	d := outbox.NewDispatcher(repo, outbox.HandlerFunc(func(ctx context.Context, e *outbox.Event) outbox.Outcome {
		inFlight.add(1)
		if v := inFlight.load(); v > maxInFlight.load() {
			maxInFlight.set(v)
		}
		time.Sleep(2 * time.Millisecond)
		inFlight.add(-1)
		return outbox.Outcome{Delivered: true}
	}), outbox.Config{Owner: "test", Concurrency: 1, BatchSize: 10})
	d.PollOnce(context.Background())
	if maxInFlight.load() > 1 {
		t.Fatalf("max concurrent Handle calls = %d, want 1 (Concurrency: 1)", maxInFlight.load())
	}
}

// int32Counter is a tiny mutex-guarded counter — avoids pulling in
// sync/atomic just for a test helper's peak-concurrency check.
type int32Counter struct {
	mu sync.Mutex
	v  int
}

func (c *int32Counter) add(d int) {
	c.mu.Lock()
	c.v += d
	c.mu.Unlock()
}
func (c *int32Counter) load() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.v
}
func (c *int32Counter) set(v int) {
	c.mu.Lock()
	c.v = v
	c.mu.Unlock()
}
