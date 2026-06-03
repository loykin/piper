package notebook

import "context"

// Driver abstracts notebook lifecycle from the server's perspective. The
// Manager stays backend-agnostic: it persists desired lifecycle state and calls
// this interface, while workers implement process, Docker, or Kubernetes
// details behind the shared worker protocol.
type Driver interface {
	// ProvisionVolume allocates backing storage for vol.
	// Bare-metal: creates a host work directory and sets vol.WorkDir/vol.WorkerID.
	// K8s: creates a PersistentVolumeClaim and reports the notebook container work dir.
	// storageSize overrides the worker-level default when non-empty.
	ProvisionVolume(ctx context.Context, vol *NotebookVolume, storageSize string) error

	// Start launches a notebook server with vol mounted.
	// Process: starts JupyterLab on the worker host.
	// Docker: starts a managed notebook container on the worker host.
	// K8s: creates or updates StatefulSet/Service resources in the worker cluster.
	Start(ctx context.Context, spec NotebookServerSpec, vol *NotebookVolume, yamlStr string) (*NotebookServer, error)

	// Stop terminates the server without touching storage.
	// Process/Docker: stops the runtime instance. K8s: scales the StatefulSet down.
	Stop(ctx context.Context, nb *NotebookServer) error

	// DeprovisionVolume permanently removes the backing storage.
	// Bare-metal: removes the host work directory. K8s: deletes the PVC.
	DeprovisionVolume(ctx context.Context, vol *NotebookVolume) error
}

// StatusSyncer is implemented by drivers that need periodic status reconciliation.
// The gRPC agent driver uses it to ask connected workers for observed state.
type StatusSyncer interface {
	SyncStatus(ctx context.Context, servers []*NotebookServer) error
}
