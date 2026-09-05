package outbox

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// Outcome is the result of Handler.Handle for a single event.
type Outcome struct {
	// Delivered, when true, marks the event StatusDelivered. Every other
	// field is ignored in that case.
	Delivered bool
	// Retryable, when Delivered is false, controls whether the Dispatcher
	// retries with backoff (true) or marks the event StatusDead immediately
	// (false) — design doc section 10.2's retryable/non-retryable error
	// lists (network timeout/reset, 408/425/429, 5xx, transient MLflow
	// "pending" states are retryable; 401/403, bad endpoint/schema, missing
	// credential, and validation/length errors are not).
	Retryable bool
	// RetryAfter, when > 0, overrides the Dispatcher's computed exponential
	// backoff — e.g. an HTTP Retry-After header (design doc section 10.2:
	// "Retry-After가 있으면 존중한다").
	RetryAfter time.Duration
	// ErrorCode and ErrorMessage are stored on the event for observability
	// and must already be redacted by the Handler (design doc section
	// 15.2) — the Dispatcher does not sanitize these further.
	ErrorCode    string
	ErrorMessage string
}

// Handler processes a single claimed outbox event. Implementations must be
// idempotent: the same event can be delivered to Handle more than once
// (at-least-once delivery — a lease can expire and be reclaimed after a
// Handle call that actually succeeded remotely but crashed before
// MarkDelivered committed).
type Handler interface {
	Handle(ctx context.Context, event *Event) Outcome
}

// HandlerFunc adapts a plain function to Handler.
type HandlerFunc func(ctx context.Context, event *Event) Outcome

func (f HandlerFunc) Handle(ctx context.Context, event *Event) Outcome { return f(ctx, event) }

// DeadNotifier is an optional interface a Handler can implement to react
// when the Dispatcher marks an event StatusDead for a reason the Handler's
// own Outcome did not decide: attempts exhausted (ev.Attempts reaching
// Config.MaxAttemptsBeforeDead) while Outcome.Retryable was still true. A
// Handler that already returns Retryable:false for a terminal failure has
// already had its chance to react synchronously before Handle returned —
// NotifyDead only fires for the attempts-exhausted case, which the Handler
// otherwise has no way to observe, since MarkDead happens entirely inside
// the Dispatcher after Handle has already returned.
type DeadNotifier interface {
	NotifyDead(ctx context.Context, event *Event, outcome Outcome)
}

// Config controls one Dispatcher's polling/claim/retry behavior (design doc
// section 13's `integrations.mlflow.*` server config maps onto this,
// though this type is integration-agnostic).
type Config struct {
	// Owner identifies this dispatcher instance for lease ownership. Should
	// be stable-ish but unique enough to detect a crashed owner's expired
	// lease (e.g. hostname+pid, or a random ID generated once at startup).
	Owner string
	// Concurrency is the number of events processed in parallel per poll
	// batch. Must be 1 for a SQLite-backed Repository (design doc section
	// 6.3) — Postgres can use a bounded value > 1.
	Concurrency int
	// BatchSize is the number of events claimed per poll.
	BatchSize int
	// PollInterval is how often the Dispatcher polls for pending/reclaimable
	// events when the previous poll found nothing to do.
	PollInterval time.Duration
	// LeaseDuration is how long a claimed event's lease is held before it
	// becomes reclaimable by another dispatcher (crash recovery).
	LeaseDuration time.Duration
	// MaxAttemptsBeforeDead caps retries: once Attempts reaches this value,
	// a Retryable failure is still marked StatusDead instead of retried
	// again (design doc section 13's `max_attempts_before_dead`).
	MaxAttemptsBeforeDead int
	// BaseBackoff and MaxBackoff bound the exponential-backoff-with-jitter
	// schedule used when Outcome.RetryAfter is not set (design doc section
	// 10.2).
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

func (c Config) withDefaults() Config {
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 20
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.LeaseDuration <= 0 {
		c.LeaseDuration = 30 * time.Second
	}
	if c.MaxAttemptsBeforeDead <= 0 {
		c.MaxAttemptsBeforeDead = 20
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = 2 * time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 5 * time.Minute
	}
	return c
}

// Dispatcher polls a Repository for claimable events and delivers them
// through a Handler, applying the retry/backoff/dead-letter policy in
// Config. It is transport/integration-agnostic — pkg/integration/mlflow's
// Exporter is the Handler that gives events their MLflow meaning.
type Dispatcher struct {
	Repo    Repository
	Handler Handler
	Config  Config

	// Now is overridable for tests. Defaults to nowUTC (time.Now().UTC()),
	// never plain time.Now — see nowUTC's doc comment for why the .UTC()
	// matters here specifically.
	Now func() time.Time
}

// nowUTC is Dispatcher's default clock. process() feeds its result straight
// into Repository.MarkRetry's next_attempt_at, which ClaimBatch later
// compares against its own time.Now().UTC() (internal/store/{sqlite,
// postgres}'s ClaimBatch). Using plain time.Now() here instead — local zone,
// carrying a monotonic reading — used to make that comparison never match
// once a server ran in a non-UTC zone: modernc.org/sqlite stores time.Time
// as its default String() form, so a MarkRetry'd next_attempt_at could come
// out as e.g. "2026-09-05 10:50:13.466... +0900 KST m=+352.48..." while
// ClaimBatch's "now" parameter was plain UTC — a retryable failure's event
// then sat in status='pending' forever, never reclaimed, never reaching
// MaxAttemptsBeforeDead, invisible to any health check that only watches
// for status='dead'. Confirmed live: an outage-simulating mlflow event
// stopped retrying entirely on a KST host and only resumed once the process
// ran under TZ=UTC.
func nowUTC() time.Time { return time.Now().UTC() }

// NewDispatcher constructs a Dispatcher with defaults applied to any unset
// Config field.
func NewDispatcher(repo Repository, handler Handler, cfg Config) *Dispatcher {
	return &Dispatcher{Repo: repo, Handler: handler, Config: cfg.withDefaults(), Now: nowUTC}
}

// Run polls in a loop until ctx is cancelled. Safe to run in its own
// goroutine (the intended usage — design doc section 4.3: MLflow calls
// never sit on the synchronous run lifecycle path).
func (d *Dispatcher) Run(ctx context.Context) {
	if d.Now == nil {
		d.Now = nowUTC
	}
	ticker := time.NewTicker(d.Config.PollInterval)
	defer ticker.Stop()
	for {
		n := d.PollOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			// Drain quickly under backlog instead of waiting a full
			// interval between every batch.
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PollOnce claims and processes a single batch, returning the number of
// events claimed (0 means nothing was due). Exported so tests and a
// caller wanting manual/synchronous control (rather than the free-running
// Run loop) can drive one cycle at a time.
func (d *Dispatcher) PollOnce(ctx context.Context) int {
	cfg := d.Config
	batch, err := d.Repo.ClaimBatch(ctx, cfg.Owner, cfg.LeaseDuration, cfg.BatchSize)
	if err != nil {
		slog.Warn("outbox dispatcher: claim batch failed", "err", err)
		return 0
	}
	if len(batch) == 0 {
		return 0
	}

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for _, ev := range batch {
		ev := ev
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d.process(ctx, ev)
		}()
	}
	wg.Wait()
	return len(batch)
}

func (d *Dispatcher) process(ctx context.Context, ev *Event) {
	cfg := d.Config
	outcome := d.Handler.Handle(ctx, ev)
	if outcome.Delivered {
		if err := d.Repo.MarkDelivered(ctx, ev.ID, cfg.Owner); err != nil {
			slog.Warn("outbox dispatcher: mark delivered failed", "event_id", ev.ID, "err", err)
		}
		return
	}

	if !outcome.Retryable || ev.Attempts >= cfg.MaxAttemptsBeforeDead {
		attemptsExhausted := outcome.Retryable && ev.Attempts >= cfg.MaxAttemptsBeforeDead
		if err := d.Repo.MarkDead(ctx, ev.ID, cfg.Owner, outcome.ErrorCode, outcome.ErrorMessage); err != nil {
			slog.Warn("outbox dispatcher: mark dead failed", "event_id", ev.ID, "err", err)
		}
		// The Handler already had a chance to react to a non-retryable
		// outcome before returning (that's the ev.Attempts < cap branch of
		// this same condition) — only the attempts-exhausted case is news to
		// it, since Handle has no visibility into Config.MaxAttemptsBeforeDead.
		if attemptsExhausted {
			if notifier, ok := d.Handler.(DeadNotifier); ok {
				notifier.NotifyDead(ctx, ev, outcome)
			}
		}
		return
	}

	delay := outcome.RetryAfter
	if delay <= 0 {
		delay = backoff(cfg.BaseBackoff, cfg.MaxBackoff, ev.Attempts)
	}
	next := d.Now().Add(delay)
	if err := d.Repo.MarkRetry(ctx, ev.ID, cfg.Owner, next, outcome.ErrorCode, outcome.ErrorMessage); err != nil {
		slog.Warn("outbox dispatcher: mark retry failed", "event_id", ev.ID, "err", err)
	}
}

// backoff computes exponential backoff with full jitter, capped at max.
// attempts is the 1-based number of attempts already made (Event.Attempts,
// already incremented by ClaimBatch for the current attempt).
func backoff(base, max time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := base
	for i := 1; i < attempts && d < max; i++ {
		d *= 2
		if d > max {
			d = max
			break
		}
	}
	if d > max {
		d = max
	}
	// Full jitter: uniform random in [0, d].
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d) + 1))
}
