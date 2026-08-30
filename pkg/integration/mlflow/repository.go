package mlflow

import "context"

// Repository is the persistence interface for MLflowIntegration and its
// mapping tables (MLflowExperimentLink, MLflowRunLink). It does not include
// the durable outbox (IntegrationOutboxEvent) — that is a separate
// follow-up task (design doc section 6.3) — nor any exporter/dispatcher
// behavior; this is the connection/credential/schema foundation only.
type Repository interface {
	// SetSSRFPolicy updates the repository's validated MLflow endpoint
	// policy, used when resolving/dialing a tracking_uri. Called once
	// during startup wiring (piper.go) after config is loaded.
	SetSSRFPolicy(policy SSRFPolicy)

	// CreateIntegration persists a new integration. If m.Default is true,
	// implementations must atomically clear Default on every other
	// integration in the same project first, so at most one default
	// integration ever exists per project (design doc section 5.1) — the
	// same "deactivate, then set" transactional idiom
	// internal/store/sqlite/credential.go's Rotate uses for
	// credential_values.active. Implementations must call Validate
	// (model.go) before writing and return ErrInvalid on failure, and
	// ErrAlreadyExists if (project_id, name) or (project_id, id) already
	// exists.
	CreateIntegration(ctx context.Context, m *MLflowIntegration) error
	// GetIntegration returns the integration by (projectID, id), or
	// (nil, nil) if it does not exist.
	GetIntegration(ctx context.Context, projectID, id string) (*MLflowIntegration, error)
	// GetIntegrationByName returns the integration by (projectID, name), or
	// (nil, nil) if it does not exist.
	GetIntegrationByName(ctx context.Context, projectID, name string) (*MLflowIntegration, error)
	// GetDefaultIntegration returns the project's Default=true integration,
	// or (nil, nil) if none is set.
	GetDefaultIntegration(ctx context.Context, projectID string) (*MLflowIntegration, error)
	// ListIntegrations returns integrations for projectID, ordered by name.
	// limit 0 means no limit (return everything); offset is only
	// meaningful when limit > 0.
	ListIntegrations(ctx context.Context, projectID string, limit, offset int) ([]*MLflowIntegration, error)
	// CountIntegrations returns the total number of integrations for
	// projectID, ignoring limit/offset.
	CountIntegrations(ctx context.Context, projectID string) (int, error)
	// UpdateIntegration replaces the stored integration identified by
	// (m.ProjectID, m.ID). Same Default-uniqueness and Validate
	// requirements as CreateIntegration. Returns ErrNotFound if no row
	// matches.
	UpdateIntegration(ctx context.Context, m *MLflowIntegration) error
	// DeleteIntegration soft-deletes the integration: it sets DeletedAt,
	// clears Enabled and Default, but does not remove the row. Per design
	// doc section 11.1, deleting an integration must not delete its
	// MLflowExperimentLink/MLflowRunLink rows (mapping history is
	// preserved) — since both mapping tables' FK reference this row, a
	// hard DELETE would cascade and erase that history, so the row itself
	// must survive. Callers that want a full purge (removing the row and
	// its mappings together) use a separate, explicit admin operation (out
	// of scope for this phase). A soft-deleted integration's name becomes
	// available for reuse by a new integration (implementations scope the
	// name-uniqueness constraint to DeletedAt IS NULL). Returns
	// ErrNotFound if no non-deleted row matches (deleting an already
	// soft-deleted or nonexistent row is indistinguishable to the caller).
	DeleteIntegration(ctx context.Context, projectID, id string) error

	// GetExperimentLink returns the experiment mapping for
	// (integrationID, projectID, piperGroupKey), or (nil, nil) if it does
	// not exist yet.
	GetExperimentLink(ctx context.Context, integrationID, projectID, piperGroupKey string) (*MLflowExperimentLink, error)
	// UpsertExperimentLink creates or replaces the mapping keyed by
	// (IntegrationID, ProjectID, PiperGroupKey) (design doc section 6.1's
	// unique key).
	UpsertExperimentLink(ctx context.Context, link *MLflowExperimentLink) error

	// GetRunLink returns the run mapping for
	// (integrationID, projectID, sourceType, sourceID), or (nil, nil) if it
	// does not exist yet.
	GetRunLink(ctx context.Context, integrationID, projectID, sourceType, sourceID string) (*MLflowRunLink, error)
	// UpsertRunLink creates or replaces the mapping keyed by
	// (IntegrationID, ProjectID, SourceType, SourceID) (design doc section
	// 6.2's unique key).
	UpsertRunLink(ctx context.Context, link *MLflowRunLink) error
	// ListRunLinksByStatus returns run links for projectID filtered by
	// syncStatus, ordered by updated_at ascending (oldest first — useful
	// for a future reconciler/backlog view). Same limit/offset convention
	// as ListIntegrations.
	ListRunLinksByStatus(ctx context.Context, projectID, syncStatus string, limit, offset int) ([]*MLflowRunLink, error)
}
