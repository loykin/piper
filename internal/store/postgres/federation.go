package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/federation"
	"github.com/loykin/piper/pkg/project"
)

type federationRepo struct{ sqlxadapter.Source }

type auditInput struct {
	homeID, eventType, memberID, projectID, actorID, detail string
	at                                                      time.Time
}

func NewFederationRepo(exec *dbstore.Executor[*sqlx.DB], source string) federation.Repository {
	return &federationRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *federationRepo) SyncConfiguredMembers(ctx context.Context, homeID string, memberIDs []string, at time.Time) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, db.Rebind(
			`UPDATE federation_members SET enabled=FALSE, status='offline', updated_at=? WHERE home_id=?`), at, homeID); err != nil {
			return err
		}
		for _, memberID := range memberIDs {
			if _, err := tx.ExecContext(ctx, db.Rebind(`
				INSERT INTO federation_members (home_id, id, enabled, status, created_at, updated_at)
				VALUES (?, ?, TRUE, 'offline', ?, ?)
				ON CONFLICT(home_id, id) DO UPDATE SET enabled=TRUE, status='offline', updated_at=EXCLUDED.updated_at`),
				homeID, memberID, at, at); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

func (r *federationRepo) SetMemberConnected(ctx context.Context, homeID, memberID string, connected bool, at time.Time) error {
	status := federation.MemberOffline
	eventType := federation.AuditMemberDisconnected
	timeColumn := "last_disconnected_at"
	if connected {
		status = federation.MemberOnline
		eventType = federation.AuditMemberConnected
		timeColumn = "last_connected_at"
	}
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		query := db.Rebind(`UPDATE federation_members SET status=?, ` + timeColumn + `=?, updated_at=? WHERE home_id=? AND id=? AND enabled=TRUE`)
		result, err := tx.ExecContext(ctx, query, status, at, at, homeID, memberID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return federation.ErrMemberNotConfigured
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(`
			INSERT INTO federation_audit_events
			(id, home_id, type, member_id, project_id, actor_id, detail, created_at)
			VALUES (?, ?, ?, ?, '', '', '', ?)`), uuid.NewString(), homeID, eventType, memberID, at); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *federationRepo) CreateProject(ctx context.Context, homeID string, value *project.Project, actorID string) error {
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	if value.OwnerMemberID == "" {
		value.OwnerMemberID = project.LocalMemberID
	}
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := requireConfiguredMemberPostgres(ctx, tx, db, homeID, value.OwnerMemberID); err != nil {
			return err
		}
		if _, err := tx.NamedExecContext(ctx, `
			INSERT INTO projects (id, name, description, owner_member_id, created_at, updated_at)
			VALUES (:id, :name, :description, :owner_member_id, :created_at, :updated_at)`, value); err != nil {
			return err
		}
		if err := insertFederationAuditPostgres(ctx, tx, db, auditInput{
			homeID: homeID, eventType: federation.AuditProjectCreated, memberID: value.OwnerMemberID,
			projectID: value.ID, actorID: actorID, at: now,
		}); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *federationRepo) SetProjectOwner(ctx context.Context, homeID, projectID, memberID, actorID string, at time.Time) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := requireConfiguredMemberPostgres(ctx, tx, db, homeID, memberID); err != nil {
			return err
		}
		var current string
		if err := tx.GetContext(ctx, &current, db.Rebind(`SELECT owner_member_id FROM projects WHERE id=?`), projectID); err != nil {
			if err == sql.ErrNoRows {
				return federation.ErrProjectNotFound
			}
			return err
		}
		if current == memberID {
			return tx.Commit()
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(`UPDATE projects SET owner_member_id=?, updated_at=? WHERE id=?`), memberID, at, projectID); err != nil {
			return err
		}
		if err := insertFederationAuditPostgres(ctx, tx, db, auditInput{
			homeID: homeID, eventType: federation.AuditProjectOwnerSet, memberID: memberID,
			projectID: projectID, actorID: actorID, detail: current, at: at,
		}); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func requireConfiguredMemberPostgres(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, homeID, memberID string) error {
	var count int
	if err := tx.GetContext(ctx, &count, db.Rebind(`SELECT COUNT(*) FROM federation_members WHERE home_id=? AND id=? AND enabled=TRUE`), homeID, memberID); err != nil {
		return err
	}
	if count != 1 {
		return federation.ErrMemberNotConfigured
	}
	return nil
}

func insertFederationAuditPostgres(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, value auditInput) error {
	_, err := tx.ExecContext(ctx, db.Rebind(`
		INSERT INTO federation_audit_events
		(id, home_id, type, member_id, project_id, actor_id, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		uuid.NewString(), value.homeID, value.eventType, value.memberID, value.projectID, value.actorID, value.detail, value.at)
	return err
}

func (r *federationRepo) ListMembers(ctx context.Context, homeID string) ([]*federation.Member, error) {
	var members []*federation.Member
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &members, db.Rebind(`
			SELECT home_id, id, enabled, status, last_connected_at, last_disconnected_at, created_at, updated_at
			FROM federation_members WHERE home_id=? ORDER BY enabled DESC, id ASC`), homeID)
	})
	if members == nil {
		members = []*federation.Member{}
	}
	return members, err
}

func (r *federationRepo) ListAuditEvents(ctx context.Context, homeID string, limit int) ([]*federation.AuditEvent, error) {
	var events []*federation.AuditEvent
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &events, db.Rebind(`
			SELECT id, home_id, type, member_id, project_id, actor_id, detail, created_at
			FROM federation_audit_events WHERE home_id=? ORDER BY created_at DESC LIMIT ?`), homeID, limit)
	})
	if events == nil {
		events = []*federation.AuditEvent{}
	}
	return events, err
}
