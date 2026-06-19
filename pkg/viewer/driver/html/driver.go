package html

import (
	"context"

	"github.com/piper/piper/pkg/viewer"
)

// Driver serves HTML artifacts directly from the local filesystem.
// The manager materializes the artifact before calling Start, so localPath is
// always a local directory regardless of the storage backend.
type Driver struct{}

func New() *Driver { return &Driver{} }

func (d *Driver) Type() string { return "html" }

func (d *Driver) Start(_ context.Context, v *viewer.Viewer, localPath string) error {
	// No process to start. The proxy handler serves files from WorkDir (= localPath).
	v.WorkDir = localPath
	return nil
}

func (d *Driver) Stop(_ context.Context, _ *viewer.Viewer) error { return nil }
