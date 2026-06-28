package postgres

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/auth"
	"github.com/piper/piper/pkg/security"
)

// ── UserRepository ───────────────────────────────────────────────────────────

type userRepo struct{ dbstore.BaseRepo }

func NewUserRepo(exec *dbstore.Executor, source string) auth.UserRepository {
	return &userRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *userRepo) Create(ctx context.Context, u *auth.User) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO users (id, email, password_hash, system_admin, disabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q, u.ID, u.Email, u.PasswordHash, u.SystemAdmin, u.Disabled, u.CreatedAt, u.UpdatedAt)
		return err
	})
}

func (r *userRepo) GetByID(ctx context.Context, id string) (*auth.User, error) {
	var u auth.User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT id, email, password_hash, system_admin, disabled, created_at, updated_at FROM users WHERE id=?`)
		return db.GetContext(ctx, &u, q, id)
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *userRepo) GetByEmail(ctx context.Context, email string) (*auth.User, error) {
	var u auth.User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT id, email, password_hash, system_admin, disabled, created_at, updated_at FROM users WHERE email=?`)
		return db.GetContext(ctx, &u, q, email)
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *userRepo) List(ctx context.Context) ([]*auth.User, error) {
	var out []*auth.User
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out,
			`SELECT id, email, password_hash, system_admin, disabled, created_at, updated_at FROM users ORDER BY created_at DESC`)
	})
	if out == nil {
		out = []*auth.User{}
	}
	return out, err
}

func (r *userRepo) Update(ctx context.Context, u *auth.User) error {
	u.UpdatedAt = time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE users SET email=?, password_hash=?, system_admin=?, disabled=?, updated_at=? WHERE id=?`)
		_, err := db.ExecContext(ctx, q, u.Email, u.PasswordHash, u.SystemAdmin, u.Disabled, u.UpdatedAt, u.ID)
		return err
	})
}

func (r *userRepo) Delete(ctx context.Context, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM users WHERE id=?`)
		_, err := db.ExecContext(ctx, q, id)
		return err
	})
}

// ── MemberRepository ─────────────────────────────────────────────────────────

type memberRepo struct{ dbstore.BaseRepo }

func NewMemberRepo(exec *dbstore.Executor, source string) security.ProjectMemberRepository {
	return &memberRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *memberRepo) Add(ctx context.Context, m *security.ProjectMember) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO project_members (project_id, user_id, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q, m.ProjectID, m.UserID, m.Role, m.CreatedAt, m.UpdatedAt)
		return err
	})
}

func (r *memberRepo) Get(ctx context.Context, projectID, userID string) (*security.ProjectMember, error) {
	var m security.ProjectMember
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT project_id, user_id, role, created_at, updated_at FROM project_members WHERE project_id=? AND user_id=?`)
		return db.GetContext(ctx, &m, q, projectID, userID)
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *memberRepo) ListByUser(ctx context.Context, userID string) ([]*security.ProjectMember, error) {
	var out []*security.ProjectMember
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT project_id, user_id, role, created_at, updated_at FROM project_members WHERE user_id=?`)
		return db.SelectContext(ctx, &out, q, userID)
	})
	return out, err
}

func (r *memberRepo) ListByProject(ctx context.Context, projectID string) ([]*security.ProjectMember, error) {
	var out []*security.ProjectMember
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT project_id, user_id, role, created_at, updated_at FROM project_members WHERE project_id=?`)
		return db.SelectContext(ctx, &out, q, projectID)
	})
	return out, err
}

func (r *memberRepo) Update(ctx context.Context, m *security.ProjectMember) error {
	m.UpdatedAt = time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE project_members SET role=?, updated_at=? WHERE project_id=? AND user_id=?`)
		_, err := db.ExecContext(ctx, q, m.Role, m.UpdatedAt, m.ProjectID, m.UserID)
		return err
	})
}

func (r *memberRepo) Remove(ctx context.Context, projectID, userID string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM project_members WHERE project_id=? AND user_id=?`)
		_, err := db.ExecContext(ctx, q, projectID, userID)
		return err
	})
}

// ── SessionRepository ────────────────────────────────────────────────────────

type sessionRepo struct{ dbstore.BaseRepo }

func NewSessionRepo(exec *dbstore.Executor, source string) auth.SessionRepository {
	return &sessionRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *sessionRepo) Create(ctx context.Context, s *auth.Session) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO auth_sessions (id, user_id, refresh_token_hash, expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)`)
		_, err := db.ExecContext(ctx, q, s.ID, s.UserID, s.RefreshTokenHash, s.ExpiresAt, s.CreatedAt, s.LastUsedAt)
		return err
	})
}

func (r *sessionRepo) GetByTokenHash(ctx context.Context, hash string) (*auth.Session, error) {
	var s auth.Session
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT id, user_id, refresh_token_hash, expires_at, revoked_at, created_at, last_used_at FROM auth_sessions WHERE refresh_token_hash=?`)
		return db.GetContext(ctx, &s, q, hash)
	})
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *sessionRepo) Revoke(ctx context.Context, id string, at time.Time) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE auth_sessions SET revoked_at=? WHERE id=?`)
		_, err := db.ExecContext(ctx, q, at, id)
		return err
	})
}

func (r *sessionRepo) RevokeAll(ctx context.Context, userID string) error {
	now := time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE auth_sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`)
		_, err := db.ExecContext(ctx, q, now, userID)
		return err
	})
}

func (r *sessionRepo) TouchLastUsed(ctx context.Context, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE auth_sessions SET last_used_at=? WHERE id=?`)
		_, err := db.ExecContext(ctx, q, time.Now().UTC(), id)
		return err
	})
}

func (r *sessionRepo) DeleteExpired(ctx context.Context) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM auth_sessions WHERE expires_at < ?`)
		_, err := db.ExecContext(ctx, q, time.Now().UTC())
		return err
	})
}
