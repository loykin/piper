// Package backend defines execution backends for dispatched pipeline tasks.
package backend

import (
	"context"
	"errors"

	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
)

// ExecutionBackend sends a runnable task to an execution environment.
//
// Backend implementations decide where a task runs. They must not own
// orchestration policy such as retry, dependency skip, or run finalization.
type ExecutionBackend interface {
	Dispatch(ctx context.Context, task *proto.Task) error
}

// ErrPollingBackend is returned if PollingBackend is used as an active backend.
var ErrPollingBackend = errors.New("polling backend does not actively dispatch tasks")

// PollingBackend documents worker-polling mode.
//
// Polling mode is represented by a nil active backend in the queue so tasks stay
// ready until a worker calls /api/tasks/next.
type PollingBackend struct{}

// Dispatch returns ErrPollingBackend because polling workers acquire tasks via
// Queue.Next rather than active dispatch.
func (PollingBackend) Dispatch(context.Context, *proto.Task) error {
	return ErrPollingBackend
}

// LocalBackend dispatches tasks to an in-process runner.
//
// The runner still reports task completion through its configured MasterURL.
// Direct in-memory reporting belongs to the later task-runtime consolidation.
type LocalBackend struct {
	runner *runner.Runner
}

// Dispatch starts task execution asynchronously.
func (b *LocalBackend) Dispatch(ctx context.Context, task *proto.Task) error {
	go b.runner.Run(ctx, task)
	return nil
}

var _ ExecutionBackend = PollingBackend{}
var _ ExecutionBackend = (*LocalBackend)(nil)
