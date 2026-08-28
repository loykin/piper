package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/integration/mlflow"
)

type mlflowRepo struct {
	sqlxadapter.Source
	policy mlflow.SSRFPolicy
}

// NewMlflowRepo wires the repository's write-time SSRF validation to
// policy — see mlflow.SSRFPolicy's doc comment: TrackingURI is a genuine
// SSRF boundary, and this repository is the enforcement floor until a
// future exporter/dispatcher service layer exists to own
// `integrations.mlflow.*` server config itself. Callers that don't have an
// explicit policy yet (which today is every caller — see
// internal/store/store.go's buildRepos) should pass
// mlflow.DefaultSSRFPolicy(), not a zero-value SSRFPolicy{} that happens to
// equal it today: DefaultSSRFPolicy is the one place that decides what
// "strict" means, and a caller hardcoding the equivalent literal would
// silently stop tracking that decision if it ever changed.
func NewMlflowRepo(exec *dbstore.Executor[*sqlx.DB], source string, policy mlflow.SSRFPolicy) mlflow.Repository {
	return &mlflowRepo{Source: sqlxadapter.NewSource(source, exec), policy: policy}
}

const mlflowIntegrationCols = `id, project_id, name, tracking_uri, credential_ref, enabled, is_default, export_pipelines, export_notebook_executions, experiment_template, artifact_mode, created_by, created_at, updated_at, deleted_at`

func (r *mlflowRepo) CreateIntegration(ctx context.Context, m *mlflow.MLflowIntegration) error {
	if err := m.Validate(r.policy); err != nil {
		return err
	}
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if m.Default {
			if _, err := tx.ExecContext(ctx,
				`UPDATE mlflow_integrations SET is_default=FALSE, updated_at=? WHERE project_id=? AND is_default=TRUE AND deleted_at IS NULL`,
				now, m.ProjectID,
			); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO mlflow_integrations (`+mlflowIntegrationCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			m.ID, m.ProjectID, m.Name, m.TrackingURI, m.CredentialRef, m.Enabled, m.Default,
			m.ExportPipelines, m.ExportNotebookExecutions, m.ExperimentTemplate, m.ArtifactMode,
			m.CreatedBy, m.CreatedAt, m.UpdatedAt,
		)
		if err != nil {
			if mlflowSQLiteUniqueError(err) {
				return mlflow.ErrAlreadyExists
			}
			return err
		}
		return tx.Commit()
	})
}

// GetIntegration looks up by immutable (projectID, id) and deliberately does
// not filter on deleted_at: a mapping row (MLflowExperimentLink/
// MLflowRunLink) needs to resolve its owning integration's identity/name
// regardless of whether that integration has since been soft-deleted.
func (r *mlflowRepo) GetIntegration(ctx context.Context, projectID, id string) (*mlflow.MLflowIntegration, error) {
	return r.getIntegrationWhere(ctx, `project_id=? AND id=?`, projectID, id)
}

// GetIntegrationByName excludes soft-deleted rows — this is the lookup
// CreateIntegration effectively relies on to know whether a name is free to
// reuse (a soft-deleted row's name is no longer considered "taken", see the
// partial unique index on (project_id, name) WHERE deleted_at IS NULL).
func (r *mlflowRepo) GetIntegrationByName(ctx context.Context, projectID, name string) (*mlflow.MLflowIntegration, error) {
	return r.getIntegrationWhere(ctx, `project_id=? AND name=? AND deleted_at IS NULL`, projectID, name)
}

func (r *mlflowRepo) GetDefaultIntegration(ctx context.Context, projectID string) (*mlflow.MLflowIntegration, error) {
	return r.getIntegrationWhere(ctx, `project_id=? AND is_default=TRUE AND deleted_at IS NULL`, projectID)
}

func (r *mlflowRepo) getIntegrationWhere(ctx context.Context, where string, args ...any) (*mlflow.MLflowIntegration, error) {
	var m mlflow.MLflowIntegration
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &m, `SELECT `+mlflowIntegrationCols+` FROM mlflow_integrations WHERE `+where, args...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *mlflowRepo) ListIntegrations(ctx context.Context, projectID string, limit, offset int) ([]*mlflow.MLflowIntegration, error) {
	query := `SELECT ` + mlflowIntegrationCols + ` FROM mlflow_integrations WHERE project_id=? AND deleted_at IS NULL ORDER BY name`
	args := []any{projectID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var out []*mlflow.MLflowIntegration
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, query, args...)
	})
	if out == nil {
		out = []*mlflow.MLflowIntegration{}
	}
	return out, err
}

func (r *mlflowRepo) CountIntegrations(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &count, `SELECT COUNT(*) FROM mlflow_integrations WHERE project_id=? AND deleted_at IS NULL`, projectID)
	})
	return count, err
}

func (r *mlflowRepo) UpdateIntegration(ctx context.Context, m *mlflow.MLflowIntegration) error {
	if err := m.Validate(r.policy); err != nil {
		return err
	}
	now := time.Now().UTC()
	m.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if m.Default {
			if _, err := tx.ExecContext(ctx,
				`UPDATE mlflow_integrations SET is_default=FALSE, updated_at=? WHERE project_id=? AND is_default=TRUE AND id<>? AND deleted_at IS NULL`,
				now, m.ProjectID, m.ID,
			); err != nil {
				return err
			}
		}
		// deleted_at IS NULL: a soft-deleted integration can't be
		// "updated" back to life through this path — ErrNotFound below
		// matches the same signal a genuinely nonexistent row gives.
		res, err := tx.ExecContext(ctx,
			`UPDATE mlflow_integrations SET name=?, tracking_uri=?, credential_ref=?, enabled=?, is_default=?, export_pipelines=?, export_notebook_executions=?, experiment_template=?, artifact_mode=?, updated_at=?
			 WHERE project_id=? AND id=? AND deleted_at IS NULL`,
			m.Name, m.TrackingURI, m.CredentialRef, m.Enabled, m.Default,
			m.ExportPipelines, m.ExportNotebookExecutions, m.ExperimentTemplate, m.ArtifactMode,
			m.UpdatedAt, m.ProjectID, m.ID,
		)
		if err != nil {
			if mlflowSQLiteUniqueError(err) {
				return mlflow.ErrAlreadyExists
			}
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return mlflow.ErrNotFound
		}
		return tx.Commit()
	})
}

// DeleteIntegration soft-deletes: see the DeletedAt field's doc comment on
// MLflowIntegration for why this must not be a hard DELETE (both mapping
// tables' FK are ON DELETE CASCADE, which would silently erase mapping
// history a hard delete here would otherwise preserve).
func (r *mlflowRepo) DeleteIntegration(ctx context.Context, projectID, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		now := time.Now().UTC()
		res, err := db.ExecContext(ctx,
			`UPDATE mlflow_integrations SET deleted_at=?, enabled=FALSE, is_default=FALSE, updated_at=? WHERE project_id=? AND id=? AND deleted_at IS NULL`,
			now, now, projectID, id,
		)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return mlflow.ErrNotFound
		}
		return nil
	})
}

const mlflowExperimentLinkCols = `integration_id, project_id, piper_group_key, mlflow_experiment_id, mlflow_name, created_at, updated_at`

func (r *mlflowRepo) GetExperimentLink(ctx context.Context, integrationID, projectID, piperGroupKey string) (*mlflow.MLflowExperimentLink, error) {
	var link mlflow.MLflowExperimentLink
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &link,
			`SELECT `+mlflowExperimentLinkCols+` FROM mlflow_experiment_links WHERE integration_id=? AND project_id=? AND piper_group_key=?`,
			integrationID, projectID, piperGroupKey)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *mlflowRepo) UpsertExperimentLink(ctx context.Context, link *mlflow.MLflowExperimentLink) error {
	now := time.Now().UTC()
	link.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO mlflow_experiment_links (`+mlflowExperimentLinkCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(integration_id, project_id, piper_group_key) DO UPDATE SET
			 	mlflow_experiment_id=excluded.mlflow_experiment_id, mlflow_name=excluded.mlflow_name, updated_at=excluded.updated_at`,
			link.IntegrationID, link.ProjectID, link.PiperGroupKey, link.MLflowExperimentID, link.MLflowName, now, now,
		)
		return err
	})
}

const mlflowRunLinkCols = `integration_id, project_id, source_type, source_id, mlflow_experiment_id, mlflow_run_id, mlflow_run_url, sync_status, last_sequence, last_error_code, last_error_message, last_synced_at, created_at, updated_at`

func (r *mlflowRepo) GetRunLink(ctx context.Context, integrationID, projectID, sourceType, sourceID string) (*mlflow.MLflowRunLink, error) {
	var link mlflow.MLflowRunLink
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &link,
			`SELECT `+mlflowRunLinkCols+` FROM mlflow_run_links WHERE integration_id=? AND project_id=? AND source_type=? AND source_id=?`,
			integrationID, projectID, sourceType, sourceID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *mlflowRepo) UpsertRunLink(ctx context.Context, link *mlflow.MLflowRunLink) error {
	now := time.Now().UTC()
	link.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO mlflow_run_links (`+mlflowRunLinkCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(integration_id, project_id, source_type, source_id) DO UPDATE SET
			 	mlflow_experiment_id=excluded.mlflow_experiment_id, mlflow_run_id=excluded.mlflow_run_id, mlflow_run_url=excluded.mlflow_run_url,
			 	sync_status=excluded.sync_status, last_sequence=excluded.last_sequence, last_error_code=excluded.last_error_code,
			 	last_error_message=excluded.last_error_message, last_synced_at=excluded.last_synced_at, updated_at=excluded.updated_at`,
			link.IntegrationID, link.ProjectID, link.SourceType, link.SourceID, link.MLflowExperimentID, link.MLflowRunID,
			link.MLflowRunURL, link.SyncStatus, link.LastSequence, link.LastErrorCode, link.LastErrorMessage, link.LastSyncedAt,
			now, now,
		)
		return err
	})
}

func (r *mlflowRepo) ListRunLinksByStatus(ctx context.Context, projectID, syncStatus string, limit, offset int) ([]*mlflow.MLflowRunLink, error) {
	query := `SELECT ` + mlflowRunLinkCols + ` FROM mlflow_run_links WHERE project_id=? AND sync_status=? ORDER BY updated_at`
	args := []any{projectID, syncStatus}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var out []*mlflow.MLflowRunLink
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, query, args...)
	})
	if out == nil {
		out = []*mlflow.MLflowRunLink{}
	}
	return out, err
}

func mlflowSQLiteUniqueError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "constraint failed") && strings.Contains(msg, "unique")
}
