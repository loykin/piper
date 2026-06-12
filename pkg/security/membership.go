package security

import (
	"context"
	"time"
)

// ProjectMember links an identity to a project with a role.
type ProjectMember struct {
	ProjectID string    `db:"project_id"`
	UserID    string    `db:"user_id"`
	Role      string    `db:"role"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// ProjectMemberRepository persists project membership records.
type ProjectMemberRepository interface {
	Add(ctx context.Context, member *ProjectMember) error
	Get(ctx context.Context, projectID, userID string) (*ProjectMember, error)
	ListByUser(ctx context.Context, userID string) ([]*ProjectMember, error)
	ListByProject(ctx context.Context, projectID string) ([]*ProjectMember, error)
	Update(ctx context.Context, member *ProjectMember) error
	Remove(ctx context.Context, projectID, userID string) error
}

// ProjectMemberManager is the capability required by project member HTTP APIs.
type ProjectMemberManager interface {
	ListMembers(ctx context.Context, projectID string) ([]*ProjectMember, error)
	AddMember(ctx context.Context, member *ProjectMember) error
	GetMember(ctx context.Context, projectID, userID string) (*ProjectMember, error)
	UpdateMember(ctx context.Context, member *ProjectMember) error
	RemoveMember(ctx context.Context, projectID, userID string) error
}
