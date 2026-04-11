package proto

import "context"

// Dispatcher dispatches ready tasks to an external execution environment.
// Implemented by K8s Job, remote worker, etc.
// When nil, the system falls back to worker polling mode.
type Dispatcher interface {
	Dispatch(ctx context.Context, task *Task) error
}
