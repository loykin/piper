// Package backend defines execution backends for dispatched pipeline tasks.
package pipelinedispatch

import (
	"context"
	"log/slog"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
)

// ExecutionBackend sends a runnable task to an execution environment.
//
// Backend implementations decide where a task runs. They must not own
// orchestration policy such as retry, dependency skip, or run finalization.
type ExecutionBackend interface {
	Dispatch(ctx context.Context, task *proto.Task) error
}

// CancelableBackend is implemented by active backends that can stop in-flight
// work for a run, such as deleting Kubernetes Jobs.
type CancelableBackend interface {
	ExecutionBackend
	CancelRun(ctx context.Context, runID string) error
}

// TaskOwner reports the worker selected by an active backend for a task.
// It lets the queue validate worker-originated completion without owning
// backend placement details.
type TaskOwner interface {
	OwnerForTask(taskID string) string
	ReleaseTask(taskID string)
}

// RunOwner releases backend-side placement state once a run is terminal.
type RunOwner interface {
	ReleaseRun(runID string)
}

// DispatchError is returned by ExecutionBackend.Dispatch to distinguish
// retryable infrastructure failures (e.g. worker busy) from permanent ones.
// A retryable error puts the task back to ready without consuming a retry attempt.
type DispatchError struct {
	Retryable bool
	Err       error
}

func (e *DispatchError) Error() string { return e.Err.Error() }
func (e *DispatchError) Unwrap() error { return e.Err }

// LocalBackend dispatches tasks to an in-process agent.
// OnComplete is called with the TaskResult after each task finishes.
type LocalBackend struct {
	agent      *agent.Runner
	OnComplete func(ctx context.Context, result proto.TaskResult) error
}

// Dispatch starts task execution asynchronously.
func (b *LocalBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	go func() {
		result := b.agent.Run(ctx, task)
		if b.OnComplete != nil {
			if err := b.OnComplete(context.Background(), result); err != nil {
				slog.Warn("local backend complete callback failed", "task_id", task.ID, "err", err)
			}
		}
	}()
	return nil
}

var _ ExecutionBackend = (*LocalBackend)(nil)
