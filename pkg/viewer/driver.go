package viewer

import "context"

// Driver handles starting and stopping a specific type of viewer process.
type Driver interface {
	Type() string
	// Start launches the viewer. Implementations must set v.Endpoint and v.PID before returning.
	Start(ctx context.Context, v *Viewer, localPath string) error
	Stop(ctx context.Context, v *Viewer) error
}
