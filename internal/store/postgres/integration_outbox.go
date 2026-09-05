package postgres

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

// NewOutboxRepo constructs the Postgres outbox.Repository. Unlike the
// SQLite implementation, ClaimBatch here uses a single
// `UPDATE ... FROM (SELECT ... FOR UPDATE SKIP LOCKED) RETURNING` statement
// so multiple dispatcher instances (bounded Concurrency > 1, design doc
// section 6.3) can safely claim disjoint batches concurrently.
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
			if err := tx.GetContext(ctx, &seq, db.Rebind(
				`SELECT COALESCE(MAX(sequence), 0) + 1 FROM integration_outbox_events WHERE integration_id=? AND aggregate_type=? AND aggregate_id=?`),
				event.IntegrationID, event.AggregateType, event.AggregateID,
			); err != nil {
				return err
			}
			event.Sequence = seq
		}

		_, err = tx.ExecContext(ctx, db.Rebind(
			`INSERT INTO integration_outbox_events (`+outboxCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', NULL, '', '', ?, NULL)`),
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
		leaseExpires := now.Add(leaseDuration)
		query := db.Rebind(`
			UPDATE integration_outbox_events e
			SET status='delivering', lease_owner=?, lease_expires_at=?, attempts=attempts+1
			FROM (
				SELECT c.id FROM integration_outbox_events c
				WHERE ((c.status = 'pending' AND c.next_attempt_at <= ?)
				    OR (c.status = 'delivering' AND c.lease_expires_at IS NOT NULL AND c.lease_expires_at < ?))
				  AND c.sequence = (
				     SELECT MIN(c2.sequence) FROM integration_outbox_events c2
				     WHERE c2.integration_id = c.integration_id
				       AND c2.aggregate_type = c.aggregate_type
				       AND c2.aggregate_id = c.aggregate_id
				       AND c2.status IN ('pending', 'delivering')
				  )
				ORDER BY c.next_attempt_at ASC
				LIMIT ?
				FOR UPDATE SKIP LOCKED
			) claimed
			WHERE e.id = claimed.id
			RETURNING ` + qualifyOutboxCols("e"))
		rows, err := db.QueryxContext(ctx, query, owner, leaseExpires, now, now, limit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var ev outbox.Event
			if err := rows.StructScan(&ev); err != nil {
				return err
			}
			claimed = append(claimed, &ev)
		}
		return rows.Err()
	})
	return claimed, err
}

// qualifyOutboxCols returns outboxCols with each column prefixed by
// alias., needed because the RETURNING clause in ClaimBatch's UPDATE ...
// FROM otherwise resolves bare column names ambiguously (they exist on
// both the target row and the "claimed" derived table).
func qualifyOutboxCols(alias string) string {
	cols := []string{"id", "integration_id", "project_id", "aggregate_type", "aggregate_id", "sequence", "event_type", "payload_json", "status", "attempts", "next_attempt_at", "lease_owner", "lease_expires_at", "last_error_code", "last_error", "created_at", "delivered_at"}
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += alias + "." + c
	}
	return out
}

func (r *outboxRepo) MarkDelivered(ctx context.Context, id, owner string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		now := time.Now().UTC()
		res, err := db.ExecContext(ctx, db.Rebind(
			`UPDATE integration_outbox_events SET status='delivered', delivered_at=?, lease_owner='', lease_expires_at=NULL
			 WHERE id=? AND lease_owner=? AND status='delivering'`),
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

// MarkRetry normalizes nextAttemptAt to UTC before storing it — see the
// sqlite implementation's doc comment for why (the on-disk invariant
// shouldn't depend on every caller remembering to call .UTC() itself).
// Postgres's timestamptz binding isn't vulnerable to sqlite's specific
// text-serialization bug, but keeping the two implementations' stored
// invariant identical is cheap and avoids the next person assuming it only
// matters for one backend.
func (r *outboxRepo) MarkRetry(ctx context.Context, id, owner string, nextAttemptAt time.Time, errorCode, errorMessage string) error {
	nextAttemptAt = nextAttemptAt.UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, db.Rebind(
			`UPDATE integration_outbox_events SET status='pending', next_attempt_at=?, lease_owner='', lease_expires_at=NULL, last_error_code=?, last_error=?
			 WHERE id=? AND lease_owner=? AND status='delivering'`),
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
		res, err := db.ExecContext(ctx, db.Rebind(
			`UPDATE integration_outbox_events SET status='dead', lease_owner='', lease_expires_at=NULL, last_error_code=?, last_error=?
			 WHERE id=? AND lease_owner=? AND status='delivering'`),
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
		res, err := db.ExecContext(ctx, db.Rebind(
			`UPDATE integration_outbox_events SET status='disabled', lease_owner='', lease_expires_at=NULL
			 WHERE integration_id=? AND status IN ('pending', 'delivering')`),
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
		return db.GetContext(ctx, &count, db.Rebind(
			`SELECT COUNT(*) FROM integration_outbox_events WHERE integration_id=? AND status=?`),
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
		// same reasoning as OldestPending's doc comment; ordering by
		// (integration_id, created_at) lets one pass keep only the first
		// (oldest) row seen per integration_id.
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
	// MIN(created_at) — see the SQLite implementation's identical doc
	// comment; Postgres's driver doesn't share that quirk, but keeping the
	// same query shape on both backends is simpler to reason about than
	// diverging for one.
	var t time.Time
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		err := db.GetContext(ctx, &t, db.Rebind(
			`SELECT created_at FROM integration_outbox_events WHERE integration_id=? AND status='pending' ORDER BY created_at ASC LIMIT 1`),
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
		return db.SelectContext(ctx, &out, db.Rebind(
			`SELECT `+outboxCols+` FROM integration_outbox_events WHERE integration_id=? AND aggregate_type=? AND aggregate_id=? ORDER BY sequence ASC`),
			integrationID, aggregateType, aggregateID,
		)
	})
	if out == nil {
		out = []*outbox.Event{}
	}
	return out, err
}
