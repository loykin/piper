// Package backend defines execution backends for dispatched pipeline tasks.
package backend

import (
	"context"

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

// CancelableBackend is implemented by active backends that can stop in-flight
// work for a run, such as deleting Kubernetes Jobs.
type CancelableBackend interface {
	ExecutionBackend
	CancelRun(ctx context.Context, runID string) error
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

var _ ExecutionBackend = (*LocalBackend)(nil)
