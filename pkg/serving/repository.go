package serving

import "context"

// Repository is the persistence interface for Service records.
type Repository interface {
	Create(ctx context.Context, svc *Service) error
	Get(ctx context.Context, projectID, name string) (*Service, error)
	Update(ctx context.Context, svc *Service) error
	Upsert(ctx context.Context, svc *Service) error
	SetStatus(ctx context.Context, projectID, name, status string) error
	SetStatusEndpoint(ctx context.Context, projectID, name, status, endpoint string) error
	List(ctx context.Context, projectID string) ([]*Service, error)
	ListByWorker(ctx context.Context, workerID string) ([]*Service, error)
	Delete(ctx context.Context, projectID, name string) error
	ListHistory(ctx context.Context, projectID string) ([]*ServiceHistory, error)
}
