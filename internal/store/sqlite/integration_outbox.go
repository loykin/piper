package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/integration/outbox"
)

type outboxRepo struct {
	sqlxadapter.Source
}

// NewOutboxRepo constructs the SQLite outbox.Repository. See
// pkg/integration/outbox.Repository's doc comment: SQLite is expected to
// run with dispatcher concurrency 1, so ClaimBatch here uses a plain
// SELECT-then-UPDATE under the existing single-writer dbstore executor
// rather than a `SELECT ... FOR UPDATE SKIP LOCKED` (SQLite doesn't support
// that clause, and doesn't need to at concurrency 1).
func NewOutboxRepo(exec *dbstore.Executor[*sqlx.DB], source string) outbox.Repository {
	return &outboxRepo{Source: sqlxadapter.NewSource(source, exec)}
}

const outboxCols = `id, integration_id, project_id, aggregate_type, aggregate_id, sequence, event_type, payload_json, status, attempts, next_attempt_at, lease_owner, lease_expires_at, last_error_code, last_error, created_at, delivered_at`

func (r *outboxRepo) Enqueue(ctx context.Context, event *outbox.Event) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	event.Status = string(outbox.StatusPending)
	event.Attempts = 0
	event.NextAttemptAt = now
	event.CreatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		seq := event.Sequence
		if seq == 0 {
			if err := tx.GetContext(ctx, &seq,
				`SELECT COALESCE(MAX(sequence), 0) + 1 FROM integration_outbox_events WHERE integration_id=? AND aggregate_type=? AND aggregate_id=?`,
				event.IntegrationID, event.AggregateType, event.AggregateID,
			); err != nil {
				return err
			}
			event.Sequence = seq
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO integration_outbox_events (`+outboxCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', NULL, '', '', ?, NULL)`,
			event.ID, event.IntegrationID, event.ProjectID, event.AggregateType, event.AggregateID, event.Sequence,
			event.EventType, event.PayloadJSON, event.Status, event.Attempts, event.NextAttemptAt, event.CreatedAt,
		)
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *outboxRepo) ClaimBatch(ctx context.Context, owner string, leaseDuration time.Duration, limit int) ([]*outbox.Event, error) {
	if limit <= 0 {
		return nil, nil
	}
	var claimed []*outbox.Event
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		now := time.Now().UTC()
		var candidates []*outbox.Event
		// Only the earliest not-yet-terminal event per aggregate is
		// claimable — the design doc section 10.3 ordering gate.
		err := db.SelectContext(ctx, &candidates,
			`SELECT `+outboxCols+` FROM integration_outbox_events e
			 WHERE (
			    (e.status = 'pending' AND e.next_attempt_at <= ?)
			    OR (e.status = 'delivering' AND e.lease_expires_at IS NOT NULL AND e.lease_expires_at < ?)
			 )
			 AND e.sequence = (
			    SELECT MIN(e2.sequence) FROM integration_outbox_events e2
			    WHERE e2.integration_id = e.integration_id
			      AND e2.aggregate_type = e.aggregate_type
			      AND e2.aggregate_id = e.aggregate_id
			      AND e2.status IN ('pending', 'delivering')
			 )
			 ORDER BY e.next_attempt_at ASC
			 LIMIT ?`,
			now, now, limit,
		)
		if err != nil {
			return err
		}
		leaseExpires := now.Add(leaseDuration)
		for _, ev := range candidates {
			res, err := db.ExecContext(ctx,
				`UPDATE integration_outbox_events SET status='delivering', lease_owner=?, lease_expires_at=?, attempts=attempts+1
				 WHERE id=? AND (status='pending' OR (status='delivering' AND lease_expires_at < ?))`,
				owner, leaseExpires, ev.ID, now,
			)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				// Lost the race to another claimant (shouldn't happen at
				// dispatcher concurrency 1, but harmless if it does).
				continue
			}
			ev.Status = string(outbox.StatusDelivering)
			ev.LeaseOwner = owner
			ev.LeaseExpiresAt = &leaseExpires
			ev.Attempts++
			claimed = append(claimed, ev)
		}
		return nil
	})
	return claimed, err
}

func (r *outboxRepo) MarkDelivered(ctx context.Context, id, owner string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		now := time.Now().UTC()
		res, err := db.ExecContext(ctx,
			`UPDATE integration_outbox_events SET status='delivered', delivered_at=?, lease_owner='', lease_expires_at=NULL
			 WHERE id=? AND lease_owner=? AND status='delivering'`,
			now, id, owner,
		)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return outbox.ErrNotFound
		}
		return nil
	})
}

// MarkRetry normalizes nextAttemptAt to UTC before storing it. modernc.org/
// sqlite persists a time.Time as its default String() form rather than a
// zone-independent representation, so a caller-supplied local-zone value
// (worse, one still carrying a monotonic reading, which time.Now() without
// .UTC() does) would be stored as e.g. "...+0900 KST m=+352.48..." — text
// that never again compares <= against ClaimBatch's own time.Now().UTC()
// parameter. The practical effect was a retryable event stuck in
// status='pending' forever after its first failure on any non-UTC host,
// invisible to a health check that only watches status='dead'. Normalizing
// here means MarkRetry's on-disk invariant doesn't depend on every current
// and future caller remembering to call .UTC() itself (outbox.Dispatcher
// does, as of the fix for this — see its Now field's doc comment — but this
// is the layer where the bug actually manifested).
func (r *outboxRepo) MarkRetry(ctx context.Context, id, owner string, nextAttemptAt time.Time, errorCode, errorMessage string) error {
	nextAttemptAt = nextAttemptAt.UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx,
			`UPDATE integration_outbox_events SET status='pending', next_attempt_at=?, lease_owner='', lease_expires_at=NULL, last_error_code=?, last_error=?
			 WHERE id=? AND lease_owner=? AND status='delivering'`,
			nextAttemptAt, errorCode, errorMessage, id, owner,
		)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return outbox.ErrNotFound
		}
		return nil
	})
}

func (r *outboxRepo) MarkDead(ctx context.Context, id, owner string, errorCode, errorMessage string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx,
			`UPDATE integration_outbox_events SET status='dead', lease_owner='', lease_expires_at=NULL, last_error_code=?, last_error=?
			 WHERE id=? AND lease_owner=? AND status='delivering'`,
			errorCode, errorMessage, id, owner,
		)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return outbox.ErrNotFound
		}
		return nil
	})
}

func (r *outboxRepo) DisableIntegrationEvents(ctx context.Context, integrationID string) (int, error) {
	var n int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx,
			`UPDATE integration_outbox_events SET status='disabled', lease_owner='', lease_expires_at=NULL
			 WHERE integration_id=? AND status IN ('pending', 'delivering')`,
			integrationID,
		)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}

func (r *outboxRepo) CountByStatus(ctx context.Context, integrationID, status string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &count,
			`SELECT COUNT(*) FROM integration_outbox_events WHERE integration_id=? AND status=?`,
			integrationID, status,
		)
	})
	return count, err
}

func (r *outboxRepo) Backlog(ctx context.Context, integrationIDs []string) (map[string]outbox.Backlog, error) {
	out := make(map[string]outbox.Backlog, len(integrationIDs))
	if len(integrationIDs) == 0 {
		return out, nil
	}
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		countQuery, countArgs, err := sqlx.In(
			`SELECT integration_id, status, COUNT(*) AS n FROM integration_outbox_events
			 WHERE integration_id IN (?) AND status IN ('pending', 'dead')
			 GROUP BY integration_id, status`, integrationIDs)
		if err != nil {
			return err
		}
		var counts []struct {
			IntegrationID string `db:"integration_id"`
			Status        string `db:"status"`
			N             int    `db:"n"`
		}
		if err := db.SelectContext(ctx, &counts, db.Rebind(countQuery), countArgs...); err != nil {
			return err
		}
		for _, c := range counts {
			b := out[c.IntegrationID]
			switch c.Status {
			case string(outbox.StatusPending):
				b.Pending = c.N
			case string(outbox.StatusDead):
				b.Dead = c.N
			}
			out[c.IntegrationID] = b
		}

		// A plain ordered SELECT rather than MIN(created_at) per group —
		// same modernc.org/sqlite type-scanning reason as OldestPending's
		// doc comment; ordering by (integration_id, created_at) lets one
		// pass keep only the first (oldest) row seen per integration_id.
		oldestQuery, oldestArgs, err := sqlx.In(
			`SELECT integration_id, created_at FROM integration_outbox_events
			 WHERE integration_id IN (?) AND status = 'pending'
			 ORDER BY integration_id, created_at ASC`, integrationIDs)
		if err != nil {
			return err
		}
		var oldest []struct {
			IntegrationID string    `db:"integration_id"`
			CreatedAt     time.Time `db:"created_at"`
		}
		if err := db.SelectContext(ctx, &oldest, db.Rebind(oldestQuery), oldestArgs...); err != nil {
			return err
		}
		seen := make(map[string]bool, len(integrationIDs))
		for _, o := range oldest {
			if seen[o.IntegrationID] {
				continue
			}
			seen[o.IntegrationID] = true
			b := out[o.IntegrationID]
			t := o.CreatedAt
			b.OldestPending = &t
			out[o.IntegrationID] = b
		}
		return nil
	})
	return out, err
}

func (r *outboxRepo) OldestPending(ctx context.Context, integrationID string) (*time.Time, error) {
	// A plain column SELECT (ORDER BY ... LIMIT 1) rather than
	// MIN(created_at): modernc.org/sqlite's type-aware time.Time scanning
	// only applies to a query result that still carries the column's
	// declared type metadata, which an aggregate function's result loses —
	// MIN(created_at) comes back as a bare string database/sql can't Scan
	// into time.Time/sql.NullTime, while a direct column read works.
	var t time.Time
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		err := db.GetContext(ctx, &t,
			`SELECT created_at FROM integration_outbox_events WHERE integration_id=? AND status='pending' ORDER BY created_at ASC LIMIT 1`,
			integrationID,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if t.IsZero() {
		return nil, nil
	}
	return &t, nil
}

func (r *outboxRepo) ListByAggregate(ctx context.Context, integrationID, aggregateType, aggregateID string) ([]*outbox.Event, error) {
	var out []*outbox.Event
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT `+outboxCols+` FROM integration_outbox_events WHERE integration_id=? AND aggregate_type=? AND aggregate_id=? ORDER BY sequence ASC`,
			integrationID, aggregateType, aggregateID,
		)
	})
	if out == nil {
		out = []*outbox.Event{}
	}
	return out, err
}
