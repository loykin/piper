package security

import "context"

// User is the application-facing representation of a user account.
type User struct {
	ID          string
	Email       string
	DisplayName string
	SystemAdmin bool
	Disabled    bool
}

// CreateUserInput contains fields supported by Piper's built-in user manager.
type CreateUserInput struct {
	Email       string
	Password    string
	DisplayName string
	SystemAdmin bool
}

// UserDirectory provides read-only user discovery.
type UserDirectory interface {
	GetUser(ctx context.Context, userID string) (*User, error)
	ListUsers(ctx context.Context) ([]*User, error)
}

// UserManager provides optional user lifecycle management.
// SSO deployments commonly omit this capability.
type UserManager interface {
	CreateUser(ctx context.Context, input CreateUserInput) (*User, error)
	DeleteUser(ctx context.Context, userID string) error
}
