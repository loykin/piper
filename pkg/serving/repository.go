package serving

import "context"

// Repository is the persistence interface for Service records.
type Repository interface {
	Create(ctx context.Context, svc *Service) error
	Get(ctx context.Context, name string) (*Service, error)
	Update(ctx context.Context, svc *Service) error
	Upsert(ctx context.Context, svc *Service) error
	SetStatus(ctx context.Context, name, status string) error
	List(ctx context.Context) ([]*Service, error)
	Delete(ctx context.Context, name string) error
}
