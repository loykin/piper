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
	// List returns services for projectID, newest first. limit 0 means no
	// limit (return everything); offset is only meaningful when limit > 0.
	List(ctx context.Context, projectID string, limit, offset int) ([]*Service, error)
	// Count returns the total number of services for projectID, ignoring
	// limit/offset.
	Count(ctx context.Context, projectID string) (int, error)
	Delete(ctx context.Context, projectID, name string) error
	// AppendHistory records svc as a past deployment in service_history,
	// stamped with the current time as its stopped_at/replaced-at moment.
	// Called both when a service is deleted and, before Deploy overwrites the
	// current row, whenever a redeploy replaces an already-running version —
	// so no version a service ever ran is silently lost on the next deploy.
	AppendHistory(ctx context.Context, svc *Service) error
	// ListHistory returns service history for projectID, most recently
	// stopped first. Same limit/offset convention as List.
	ListHistory(ctx context.Context, projectID string, limit, offset int) ([]*ServiceHistory, error)
	// CountHistory returns the total number of service history rows for
	// projectID, ignoring limit/offset.
	CountHistory(ctx context.Context, projectID string) (int, error)
}
