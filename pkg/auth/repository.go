package auth

import (
	"context"
	"time"
)

// UserRepository is the persistence interface for User records.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	// List returns accounts ordered by created_at. limit 0 means no limit
	// (return everything); offset is only meaningful when limit > 0.
	List(ctx context.Context, limit, offset int) ([]*User, error)
	// Count returns the total number of accounts, ignoring limit/offset.
	Count(ctx context.Context) (int, error)
	Update(ctx context.Context, u *User) error
	Delete(ctx context.Context, id string) error
}

// SessionRepository manages auth sessions.
type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	GetByTokenHash(ctx context.Context, hash string) (*Session, error)
	Revoke(ctx context.Context, id string, at time.Time) error
	RevokeAll(ctx context.Context, userID string) error
	TouchLastUsed(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context) error
	RecordLoginAttempt(ctx context.Context, attempt *LoginAttempt) error
}
