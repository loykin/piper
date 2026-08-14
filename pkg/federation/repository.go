package federation

import (
	"context"
	"errors"
	"time"

	"github.com/loykin/piper/pkg/project"
)

var ErrMemberNotConfigured = errors.New("federation: member is not configured")
var ErrProjectNotFound = errors.New("federation: project not found")

// Repository persists only the Home control-plane directory and its audit
// trail. Enrollment secrets and Member-owned execution records are excluded.
type Repository interface {
	SyncConfiguredMembers(ctx context.Context, homeID string, memberIDs []string, at time.Time) error
	SetMemberConnected(ctx context.Context, homeID, memberID string, connected bool, at time.Time) error
	CreateProject(ctx context.Context, homeID string, value *project.Project, actorID string) error
	SetProjectOwner(ctx context.Context, homeID, projectID, memberID, actorID string, at time.Time) error
	ListMembers(ctx context.Context, homeID string) ([]*Member, error)
	ListAuditEvents(ctx context.Context, homeID string, limit int) ([]*AuditEvent, error)
}
