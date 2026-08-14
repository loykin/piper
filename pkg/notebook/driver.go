package notebook

import (
	"context"
)

// Driver abstracts notebook lifecycle from the server's perspective. The
// Manager stays backend-agnostic: it persists desired lifecycle state and
// calls this interface, while the direct-runtime driver implements process,
// Docker, or Kubernetes details for the installation's configured runtime.
type Driver interface {
	// ProvisionVolume allocates backing storage for vol.
	// Bare-metal: creates a host work directory and sets vol.WorkDir.
	// K8s: creates a PersistentVolumeClaim and reports the notebook container work dir.
	ProvisionVolume(ctx context.Context, vol *NotebookVolume, spec Notebook) error

	// Start launches a notebook server with vol mounted.
	// Process: starts JupyterLab on the Piper host.
	// Docker: starts a managed notebook container on the Piper host.
	// K8s: creates or updates StatefulSet/Service resources in the cluster.
	Start(ctx context.Context, spec Notebook, vol *NotebookVolume, yamlStr string) (*NotebookServer, error)

	// Stop terminates the server without touching storage.
	// Process/Docker: stops the runtime instance. K8s: scales the StatefulSet down.
	Stop(ctx context.Context, nb *NotebookServer) error

	// DeprovisionVolume permanently removes the backing storage.
	// Bare-metal: removes the host work directory. K8s: deletes the PVC.
	DeprovisionVolume(ctx context.Context, vol *NotebookVolume) error
}
