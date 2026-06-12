package auth

import (
	"context"
	"time"
)

// UserRepository is the persistence interface for User records.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context) ([]*User, error)
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
}
