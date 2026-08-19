package federation

import (
	"context"
	"fmt"
	"time"

	"github.com/loykin/piper/pkg/project"
)

// Service wraps the federation Repository with the project-existence and
// default-member-ID logic Home needs around it — the business-logic layer
// behind the piper.Piper federation methods and the /api/federation routes.
type Service struct {
	projects project.Repository
	repo     Repository
}

// NewService creates a federation Service.
func NewService(projects project.Repository, repo Repository) *Service {
	return &Service{projects: projects, repo: repo}
}

// SetProjectOwner updates Home's authoritative Project directory and audit
// trail atomically. It returns false when the Project does not exist yet;
// creation can subsequently apply the same owner through project.OwnerResolver.
func (s *Service) SetProjectOwner(ctx context.Context, homeID, projectID, memberID, actorID string) (bool, error) {
	projectRecord, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return false, err
	}
	if projectRecord == nil {
		return false, nil
	}
	if memberID == "" {
		memberID = project.LocalMemberID
	}
	if projectRecord.OwnerMemberID == memberID {
		return true, nil
	}
	if s.repo == nil {
		return true, fmt.Errorf("federation repository is unavailable")
	}
	return true, s.repo.SetProjectOwner(ctx, homeID, projectID, memberID, actorID, time.Now().UTC())
}

// CreateProject creates a project record through the federation audit path.
func (s *Service) CreateProject(ctx context.Context, homeID string, value *project.Project, actorID string) error {
	if s.repo == nil {
		return fmt.Errorf("federation repository is unavailable")
	}
	return s.repo.CreateProject(ctx, homeID, value, actorID)
}

// SyncMembers reconciles Home's non-secret Member directory with the
// configured enrollment identities. Previously configured Members remain as
// disabled history records; all connections start offline after restart.
func (s *Service) SyncMembers(ctx context.Context, homeID string, memberIDs []string) error {
	if s.repo == nil {
		return fmt.Errorf("federation repository is unavailable")
	}
	return s.repo.SyncConfiguredMembers(ctx, homeID, memberIDs, time.Now().UTC())
}

// SetMemberConnected atomically updates the Member directory and appends its
// connection audit event.
func (s *Service) SetMemberConnected(ctx context.Context, homeID, memberID string, connected bool) error {
	if s.repo == nil {
		return fmt.Errorf("federation repository is unavailable")
	}
	return s.repo.SetMemberConnected(ctx, homeID, memberID, connected, time.Now().UTC())
}
