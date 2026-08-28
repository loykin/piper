package execution

import (
	"context"
	"time"
)

// Repository is the persistence interface for KernelSession and
// NotebookExecution records. Implementations live in
// internal/store/sqlite and internal/store/postgres, following the same
// pattern as pkg/integration/mlflow.Repository.
type Repository interface {
	// CreateKernelSession persists a new kernel session.
	CreateKernelSession(ctx context.Context, k *KernelSession) error
	// GetKernelSession returns the session by (projectID, id), or
	// (nil, nil) if it does not exist.
	GetKernelSession(ctx context.Context, projectID, id string) (*KernelSession, error)
	// ListKernelSessions returns sessions for (projectID, notebookName),
	// most recently created first. createdBy, when non-empty, restricts
	// results to that actor's own sessions (design doc §7.2: "호출자가
	// 소유한 세션 목록; admin은 전체" — callers pass "" for admin/list-all).
	ListKernelSessions(ctx context.Context, projectID, notebookName, createdBy string, limit, offset int) ([]*KernelSession, error)
	// UpdateKernelSession persists changes to an existing session
	// (status, LastActivityAt, ClosedAt, ...). Returns ErrNotFound if no
	// row matches (ProjectID, ID).
	UpdateKernelSession(ctx context.Context, k *KernelSession) error
	// CountOpenKernelSessions returns the number of non-closed/failed
	// sessions for (projectID, notebookName) — the admission check behind
	// notebook_execution.max_kernels_per_notebook (design doc §11.1).
	CountOpenKernelSessions(ctx context.Context, projectID, notebookName string) (int, error)
	// ListStaleKernelSessions returns open sessions whose LastActivityAt is
	// before cutoff, across every project — feeds the kernel_idle_ttl sweep.
	ListStaleKernelSessions(ctx context.Context, cutoff time.Time) ([]*KernelSession, error)

	// CreateExecution persists a new execution record.
	CreateExecution(ctx context.Context, e *NotebookExecution) error
	// GetExecution returns the execution by (projectID, id), or (nil, nil)
	// if it does not exist.
	GetExecution(ctx context.Context, projectID, id string) (*NotebookExecution, error)
	// FindExecutionByIdempotencyKey looks up a prior execution created by
	// the same actor against the same notebook with the same
	// Idempotency-Key (design doc §7.3's replay scope: "같은 프로젝트,
	// actor, target, key"). Returns (nil, nil) if none exists yet.
	FindExecutionByIdempotencyKey(ctx context.Context, projectID, notebookName, requestedBy, idempotencyKey string) (*NotebookExecution, error)
	// ListExecutions returns executions for (projectID, notebookName),
	// most recently queued first.
	ListExecutions(ctx context.Context, projectID, notebookName string, limit, offset int) ([]*NotebookExecution, error)
	// CountExecutions returns the total number of executions for
	// (projectID, notebookName), ignoring limit/offset.
	CountExecutions(ctx context.Context, projectID, notebookName string) (int, error)
	// UpdateExecution persists changes to an existing execution. Returns
	// ErrNotFound if no row matches (ProjectID, ID).
	UpdateExecution(ctx context.Context, e *NotebookExecution) error
	// CountRunningExecutions returns the number of executions for
	// (projectID, notebookName) currently in StatusRunning — the admission
	// check behind notebook_execution.max_running_per_notebook.
	CountRunningExecutions(ctx context.Context, projectID, notebookName string) (int, error)
	// CountQueuedExecutions returns the number of executions for projectID
	// currently in StatusQueued or StatusAwaitingApproval — the admission
	// check behind notebook_execution.max_queued_per_project.
	CountQueuedExecutions(ctx context.Context, projectID string) (int, error)
	// ListExecutionsByStatus returns every execution (across all projects)
	// currently in one of statuses — used by recovery at Piper startup
	// (design doc §11.2) to find queued/running/cancelling rows left over
	// from before a restart.
	ListExecutionsByStatus(ctx context.Context, statuses []string) ([]*NotebookExecution, error)

	// GetExecutionPolicy returns the project-level notebook_execution.mcp_policy
	// override, or ("", nil) if the project has no override (callers fall
	// back to the system-wide default from config).
	GetExecutionPolicy(ctx context.Context, projectID string) (string, error)
	// SetExecutionPolicy creates or replaces the project-level policy
	// override.
	SetExecutionPolicy(ctx context.Context, projectID, policy, updatedBy string) error
}
