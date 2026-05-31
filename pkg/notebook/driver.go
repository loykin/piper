package notebook

import "context"

// Driver abstracts the backend that runs notebook server processes and manages
// their persistent storage. Implement this interface for local-process mode or
// Kubernetes (PVC + Pod) mode — the Manager is identical in both cases.
type Driver interface {
	// ProvisionVolume allocates backing storage for vol.
	// Local: picks a worker node, creates the directory, sets vol.WorkDir and vol.WorkerID.
	// K8s: creates a PersistentVolumeClaim, sets vol.WorkDir to the container mountPath.
	ProvisionVolume(ctx context.Context, vol *NotebookVolume) error

	// Start launches a notebook server with vol mounted.
	// Local: runs jupyter-lab on vol's worker with --notebook-dir=vol.WorkDir.
	// K8s: creates a Pod/StatefulSet with the PVC mounted.
	Start(ctx context.Context, spec NotebookServerSpec, vol *NotebookVolume, yamlStr string) (*NotebookServer, error)

	// Stop terminates the server without touching storage.
	// Local: kills the process. K8s: deletes the Pod.
	Stop(ctx context.Context, nb *NotebookServer) error

	// DeprovisionVolume permanently removes the backing storage.
	// Local: removes the directory on vol's worker. K8s: deletes the PVC.
	DeprovisionVolume(ctx context.Context, vol *NotebookVolume) error
}
