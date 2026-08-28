package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"

	"github.com/loykin/piper/pkg/notebook/execution"
)

type executionRepo struct{ sqlxadapter.Source }

// NewNotebookExecutionRepo constructs the PostgreSQL implementation of
// execution.Repository. Same pattern as NewNotebookRepo in notebook.go:
// every query is written with "?" bindvars and rebound via db.Rebind before
// use, matching this package's existing convention.
func NewNotebookExecutionRepo(exec *dbstore.Executor[*sqlx.DB], source string) execution.Repository {
	return &executionRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func qmarks(n int) string {
	if n <= 0 {
		return ""
	}
	s := strings.Repeat("?, ", n)
	return s[:len(s)-2]
}

func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key value violates unique constraint")
}

const kernelSessionCols = `id, project_id, notebook_name, notebook_path, jupyter_session_id, kernel_id, kernel_name, status, created_by, client_id, last_activity_at, created_at, closed_at`

func (r *executionRepo) CreateKernelSession(ctx context.Context, k *execution.KernelSession) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO kernel_sessions (` + kernelSessionCols + `) VALUES (` + qmarks(13) + `)`)
		_, err := db.ExecContext(ctx, q,
			k.ID, k.ProjectID, k.NotebookName, k.NotebookPath, k.JupyterSessionID, k.KernelID, k.KernelName,
			k.Status, k.CreatedBy, k.ClientID, k.LastActivityAt, k.CreatedAt, k.ClosedAt)
		return err
	})
}

func (r *executionRepo) GetKernelSession(ctx context.Context, projectID, id string) (*execution.KernelSession, error) {
	var k execution.KernelSession
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + kernelSessionCols + ` FROM kernel_sessions WHERE project_id=? AND id=?`)
		return db.GetContext(ctx, &k, q, projectID, id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *executionRepo) ListKernelSessions(ctx context.Context, projectID, notebookName, createdBy string, limit, offset int) ([]*execution.KernelSession, error) {
	query := `SELECT ` + kernelSessionCols + ` FROM kernel_sessions WHERE project_id=? AND notebook_name=?`
	args := []any{projectID, notebookName}
	if createdBy != "" {
		query += ` AND created_by=?`
		args = append(args, createdBy)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	var out []*execution.KernelSession
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*execution.KernelSession{}
	}
	return out, err
}

func (r *executionRepo) UpdateKernelSession(ctx context.Context, k *execution.KernelSession) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE kernel_sessions SET jupyter_session_id=?, kernel_id=?, kernel_name=?, status=?, last_activity_at=?, closed_at=? WHERE project_id=? AND id=?`)
		res, err := db.ExecContext(ctx, q,
			k.JupyterSessionID, k.KernelID, k.KernelName, k.Status, k.LastActivityAt, k.ClosedAt, k.ProjectID, k.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return execution.ErrNotFound
		}
		return nil
	})
}

func (r *executionRepo) CountOpenKernelSessions(ctx context.Context, projectID, notebookName string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COUNT(*) FROM kernel_sessions WHERE project_id=? AND notebook_name=? AND status NOT IN (?, ?)`)
		return db.GetContext(ctx, &count, q, projectID, notebookName, execution.KernelStatusClosed, execution.KernelStatusFailed)
	})
	return count, err
}

func (r *executionRepo) ListStaleKernelSessions(ctx context.Context, cutoff time.Time) ([]*execution.KernelSession, error) {
	var out []*execution.KernelSession
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + kernelSessionCols + ` FROM kernel_sessions WHERE status NOT IN (?, ?) AND last_activity_at < ?`)
		return db.SelectContext(ctx, &out, q, execution.KernelStatusClosed, execution.KernelStatusFailed, cutoff)
	})
	if out == nil {
		out = []*execution.KernelSession{}
	}
	return out, err
}

const executionCols = `id, project_id, notebook_name, notebook_path, result_path, kernel_session_id, kind, status, requested_by, client_id, idempotency_key, request_hash, source_sha256, base_content_hash, current_cell, total_cells, error_code, error_message, output_summary, approved_by, approved_at, denied_by, denied_at, queued_at, started_at, finished_at, updated_at`

func (r *executionRepo) CreateExecution(ctx context.Context, e *execution.NotebookExecution) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO notebook_executions (` + executionCols + `) VALUES (` + qmarks(27) + `)`)
		_, err := db.ExecContext(ctx, q,
			e.ID, e.ProjectID, e.NotebookName, e.NotebookPath, e.ResultPath, e.KernelSessionID, e.Kind, e.Status,
			e.RequestedBy, e.ClientID, e.IdempotencyKey, e.RequestHash, e.SourceSHA256, e.BaseContentHash,
			e.CurrentCell, e.TotalCells, e.ErrorCode, e.ErrorMessage, e.OutputSummary,
			e.ApprovedBy, e.ApprovedAt, e.DeniedBy, e.DeniedAt, e.QueuedAt, e.StartedAt, e.FinishedAt, e.UpdatedAt)
		if err != nil {
			if isUniqueConstraintErr(err) {
				return execution.ErrConflict
			}
			return err
		}
		return nil
	})
}

func (r *executionRepo) GetExecution(ctx context.Context, projectID, id string) (*execution.NotebookExecution, error) {
	var e execution.NotebookExecution
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + executionCols + ` FROM notebook_executions WHERE project_id=? AND id=?`)
		return db.GetContext(ctx, &e, q, projectID, id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *executionRepo) FindExecutionByIdempotencyKey(ctx context.Context, projectID, notebookName, requestedBy, idempotencyKey string) (*execution.NotebookExecution, error) {
	if idempotencyKey == "" {
		return nil, nil
	}
	var e execution.NotebookExecution
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT ` + executionCols + ` FROM notebook_executions WHERE project_id=? AND notebook_name=? AND requested_by=? AND idempotency_key=?`)
		return db.GetContext(ctx, &e, q, projectID, notebookName, requestedBy, idempotencyKey)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *executionRepo) ListExecutions(ctx context.Context, projectID, notebookName string, limit, offset int) ([]*execution.NotebookExecution, error) {
	query := `SELECT ` + executionCols + ` FROM notebook_executions WHERE project_id=? AND notebook_name=? ORDER BY queued_at DESC`
	args := []any{projectID, notebookName}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	var out []*execution.NotebookExecution
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*execution.NotebookExecution{}
	}
	return out, err
}

func (r *executionRepo) CountExecutions(ctx context.Context, projectID, notebookName string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COUNT(*) FROM notebook_executions WHERE project_id=? AND notebook_name=?`)
		return db.GetContext(ctx, &count, q, projectID, notebookName)
	})
	return count, err
}

func (r *executionRepo) UpdateExecution(ctx context.Context, e *execution.NotebookExecution) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE notebook_executions SET kernel_session_id=?, status=?, current_cell=?, total_cells=?, error_code=?, error_message=?, output_summary=?, approved_by=?, approved_at=?, denied_by=?, denied_at=?, started_at=?, finished_at=?, updated_at=? WHERE project_id=? AND id=?`)
		res, err := db.ExecContext(ctx, q,
			e.KernelSessionID, e.Status, e.CurrentCell, e.TotalCells, e.ErrorCode, e.ErrorMessage, e.OutputSummary,
			e.ApprovedBy, e.ApprovedAt, e.DeniedBy, e.DeniedAt, e.StartedAt, e.FinishedAt, e.UpdatedAt, e.ProjectID, e.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return execution.ErrNotFound
		}
		return nil
	})
}

func (r *executionRepo) CountRunningExecutions(ctx context.Context, projectID, notebookName string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COUNT(*) FROM notebook_executions WHERE project_id=? AND notebook_name=? AND status=?`)
		return db.GetContext(ctx, &count, q, projectID, notebookName, execution.StatusRunning)
	})
	return count, err
}

func (r *executionRepo) CountQueuedExecutions(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT COUNT(*) FROM notebook_executions WHERE project_id=? AND status IN (?, ?)`)
		return db.GetContext(ctx, &count, q, projectID, execution.StatusQueued, execution.StatusAwaitingApproval)
	})
	return count, err
}

func (r *executionRepo) ListExecutionsByStatus(ctx context.Context, statuses []string) ([]*execution.NotebookExecution, error) {
	if len(statuses) == 0 {
		return []*execution.NotebookExecution{}, nil
	}
	var out []*execution.NotebookExecution
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		query, args, err := sqlx.In(`SELECT `+executionCols+` FROM notebook_executions WHERE status IN (?) ORDER BY queued_at`, statuses)
		if err != nil {
			return err
		}
		return db.SelectContext(ctx, &out, db.Rebind(query), args...)
	})
	if out == nil {
		out = []*execution.NotebookExecution{}
	}
	return out, err
}

func (r *executionRepo) GetExecutionPolicy(ctx context.Context, projectID string) (string, error) {
	var policy string
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT mcp_policy FROM notebook_execution_policy WHERE project_id=?`)
		return db.GetContext(ctx, &policy, q, projectID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return policy, err
}

func (r *executionRepo) SetExecutionPolicy(ctx context.Context, projectID, policy, updatedBy string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`INSERT INTO notebook_execution_policy (project_id, mcp_policy, updated_by, updated_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT (project_id) DO UPDATE SET mcp_policy=excluded.mcp_policy, updated_by=excluded.updated_by, updated_at=excluded.updated_at`)
		_, err := db.ExecContext(ctx, q, projectID, policy, updatedBy, time.Now())
		return err
	})
}
