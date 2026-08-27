package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

type CredentialValidator interface {
	ValidateNotificationCredential(ctx context.Context, projectID, name string) error
}

type Service struct {
	repo        Repository
	credentials CredentialValidator
	onChanged   func(context.Context) error
}

func NewService(repo Repository, credentials CredentialValidator, onChanged func(context.Context) error) *Service {
	return &Service{repo: repo, credentials: credentials, onChanged: onChanged}
}

func (s *Service) List(ctx context.Context, projectID string, limit, offset int) ([]*Rule, error) {
	return s.repo.List(ctx, projectID, limit, offset)
}
func (s *Service) Count(ctx context.Context, projectID string) (int, error) {
	return s.repo.Count(ctx, projectID)
}
func (s *Service) Get(ctx context.Context, projectID, id string) (*Rule, error) {
	return s.repo.Get(ctx, projectID, id)
}

func (s *Service) Create(ctx context.Context, projectID, actorID string, req CreateRequest) (*Rule, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rule := &Rule{ID: uuid.NewString(), ProjectID: projectID, Name: req.Name, Source: req.Source, EventType: req.EventType, When: req.When, MetricKey: req.MetricKey, Condition: req.Condition, Notify: append([]string(nil), req.Notify...), CooldownSeconds: req.CooldownSeconds, Enabled: enabled, CreatedBy: actorID}
	if err := s.validate(ctx, rule); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, rule); err != nil {
		return nil, err
	}
	s.changed(ctx)
	return rule, nil
}

func (s *Service) Patch(ctx context.Context, projectID, id string, req PatchRequest) (*Rule, error) {
	rule, err := s.repo.Get(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		return nil, ErrNotFound
	}
	if req.Name != nil {
		rule.Name = *req.Name
	}
	if req.Source != nil {
		rule.Source = *req.Source
	}
	if req.EventType != nil {
		rule.EventType = *req.EventType
	}
	if req.When != nil {
		rule.When = *req.When
	}
	if req.MetricKey != nil {
		rule.MetricKey = *req.MetricKey
	}
	if req.Condition != nil {
		rule.Condition = *req.Condition
	}
	if req.Notify != nil {
		rule.Notify = append([]string(nil), (*req.Notify)...)
	}
	if req.CooldownSeconds != nil {
		rule.CooldownSeconds = *req.CooldownSeconds
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if err := s.validate(ctx, rule); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, rule); err != nil {
		return nil, err
	}
	s.changed(ctx)
	return rule, nil
}

func (s *Service) Delete(ctx context.Context, projectID, id string) error {
	if err := s.repo.Delete(ctx, projectID, id); err != nil {
		return err
	}
	s.changed(ctx)
	return nil
}

func (s *Service) validate(ctx context.Context, rule *Rule) error {
	if err := ValidateRule(rule); err != nil {
		return err
	}
	for _, ref := range rule.Notify {
		if s.credentials == nil {
			return fmt.Errorf("%w: credential validation is unavailable", ErrInvalid)
		}
		if err := s.credentials.ValidateNotificationCredential(ctx, rule.ProjectID, ref); err != nil {
			return fmt.Errorf("%w: notify credential %q: %v", ErrInvalid, ref, err)
		}
	}
	data, _ := json.Marshal(rule.Notify)
	rule.NotifyJSON = string(data)
	rule.Name = strings.TrimSpace(rule.Name)
	return nil
}

func (s *Service) changed(ctx context.Context) {
	if s.onChanged != nil {
		if err := s.onChanged(ctx); err != nil {
			slog.Error("alerting: refresh rules after mutation failed", "err", err)
		}
	}
}
