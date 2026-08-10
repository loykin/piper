// Package backend defines execution backends for dispatched pipeline tasks.
package pipelinedispatch

import (
	"context"

	"github.com/loykin/piper/internal/proto"
)

// RunDispatchBackend is implemented by backends that support the run-level,
// worker-owned scheduling model (pipeline.run_dispatch) — the only dispatch
// model Piper supports. AgentBackend is the sole implementation; a run's own
// scheduler (pkg/pipeline/worker/scheduler) owns dependency promotion,
// retry, and timeout for every step once DispatchRun hands it the DAG.
type RunDispatchBackend interface {
	DispatchRun(ctx context.Context, dispatch proto.RunDispatch) error
	// IsTracking reports whether this backend instance already has (or is
	// establishing) a binding for runID — used to skip a run a
	// still-live process already dispatched when resending undelivered
	// dispatches after a restart.
	IsTracking(runID string) bool
}

// CancelableBackend is a RunDispatchBackend that can relay a cancel request
// for an in-flight run to its bound worker.
type CancelableBackend interface {
	RunDispatchBackend
	CancelRun(ctx context.Context, runID string) error
}

// RunOwner releases backend-side placement state once a run is terminal —
// implemented by RunDispatchBackend backends so pipeline_db_handlers.go's
// run_finalize handler can drop its in-memory binding when a run reaches a
// terminal status, mirroring CancelRun's own cleanup for the canceled path.
type RunOwner interface {
	ReleaseRun(runID string)
}

// DispatchError is returned by RunDispatchBackend.DispatchRun to distinguish
// retryable infrastructure failures (e.g. worker busy) from permanent ones.
// A retryable error means the caller should retry without treating it as a
// permanent dispatch failure.
type DispatchError struct {
	Retryable bool
	Err       error
}

func (e *DispatchError) Error() string { return e.Err.Error() }
func (e *DispatchError) Unwrap() error { return e.Err }
