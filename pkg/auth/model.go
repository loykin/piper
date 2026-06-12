package auth

import "time"

// User is the persisted user account.
type User struct {
	ID           string    `db:"id"`
	Email        string    `db:"email"`
	PasswordHash string    `db:"password_hash"`
	SystemAdmin  bool      `db:"system_admin"`
	Disabled     bool      `db:"disabled"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

// Session holds a refresh token (stored as a hash).
type Session struct {
	ID               string     `db:"id"`
	UserID           string     `db:"user_id"`
	RefreshTokenHash string     `db:"refresh_token_hash"`
	ExpiresAt        time.Time  `db:"expires_at"`
	RevokedAt        *time.Time `db:"revoked_at"`
	CreatedAt        time.Time  `db:"created_at"`
	LastUsedAt       time.Time  `db:"last_used_at"`
}
