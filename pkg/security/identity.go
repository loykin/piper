// Package security defines authentication and authorization capabilities.
package security

import (
	"context"
	"net/http"
)

// Identity represents an authenticated caller.
type Identity struct {
	ID          string
	DisplayName string
	Email       string
	SystemAdmin bool
}

// ProjectRole is the access level a caller holds within a project.
type ProjectRole int

const (
	ProjectRoleViewer ProjectRole = iota + 1
	ProjectRoleMember
	ProjectRoleAdmin
)

// Authenticator establishes the identity associated with an HTTP request.
type Authenticator interface {
	// Authenticate extracts the caller's identity from the request.
	// Returns (nil, nil) for anonymous requests.
	Authenticate(ctx context.Context, r *http.Request) (*Identity, error)
}

// Authorizer decides which system and project operations an identity may use.
type Authorizer interface {
	// ListProjectRoles returns all project IDs the identity can access,
	// mapped to their role. Used to power the project list endpoint.
	ListProjectRoles(ctx context.Context, identity *Identity) (map[string]ProjectRole, error)

	// ProjectRole resolves the identity's role in projectID.
	// It returns an error when the identity cannot access the project.
	ProjectRole(ctx context.Context, identity *Identity, projectID string) (ProjectRole, error)

	// AuthorizeSystem checks that the identity is a system admin.
	// Returns a non-nil error (403) when access is denied.
	AuthorizeSystem(ctx context.Context, identity *Identity) error
}

// ── context helpers ──────────────────────────────────────────────────────────

type identityKey struct{}

// WithIdentity stores the authenticated identity in the context.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFromContext retrieves the identity stored by the auth middleware.
// Returns (nil, false) in trusted/no-auth mode.
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(*Identity)
	return id, ok && id != nil
}
