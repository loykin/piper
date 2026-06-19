package notebook

// FSListFilesRequest is sent from the master to a worker to list files under a directory.
type FSListFilesRequest struct {
	ProjectID string   `json:"project_id,omitempty"`
	VolumeID  string   `json:"volume_id,omitempty"`
	WorkDir   string   `json:"work_dir,omitempty"`
	Notebook  string   `json:"notebook,omitempty"` // notebook server name for Jupyter Contents API
	Token     string   `json:"token,omitempty"`    // Jupyter token for running notebooks
	Path      string   `json:"path,omitempty"`     // subpath within volume root
	Ext       []string `json:"ext,omitempty"`      // e.g. [".py", ".ipynb"]; empty = all files
	MaxFiles  int      `json:"max_files,omitempty"`
}

// FSAccessState describes the accessibility of the volume at query time.
type FSAccessState string

const (
	FSAccessReady         FSAccessState = "ready"
	FSAccessTransitioning FSAccessState = "transitioning" // starting/stopping, retry later
	FSAccessUnavailable   FSAccessState = "unavailable"   // PVC lost or unrecoverable
)

// FSListFilesResponse carries the list of relative file paths and access state.
type FSListFilesResponse struct {
	Files             []string      `json:"files"`
	State             FSAccessState `json:"state,omitempty"`
	Truncated         bool          `json:"truncated,omitempty"`
	RetryAfterSeconds int           `json:"retry_after_seconds,omitempty"`
	Message           string        `json:"message,omitempty"`
}

type WorkerProvisionVolumeRequest struct {
	VolumeID    string `json:"volume_id"`
	StorageSize string `json:"storage_size,omitempty"`
}

type WorkerProvisionVolumeResponse struct {
	WorkDir string `json:"work_dir"`
}

type WorkerStartRequest struct {
	ProjectID string `json:"project_id"`
	YAML      string `json:"yaml"`
	MasterURL string `json:"master_url,omitempty"`
	WorkDir   string `json:"work_dir"`
	VolumeID  string `json:"volume_id"`
}

type WorkerStartResponse struct {
	Token    string `json:"token"`
	WorkDir  string `json:"work_dir"`
	Endpoint string `json:"endpoint,omitempty"`
}

type WorkerStopRequest struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
}

type WorkerDeprovisionVolumeRequest struct {
	VolumeID string `json:"volume_id"`
}

type WorkerSyncStatusRequest struct {
	Targets []WorkerSyncStatusTarget `json:"targets"`
}

type WorkerSyncStatusTarget struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Port      int    `json:"port,omitempty"`
}

// WorkerSyncStatusResponse carries observed states keyed by "projectID:name".
// The composite key prevents cross-project name collisions when a single
// worker hosts notebooks from multiple projects.
type WorkerSyncStatusResponse struct {
	Statuses map[string]string `json:"statuses"`
}

// WorkerStatusUpdate is the backend-neutral observed state reported by a worker.
// Runtime-specific workers populate the fields they can observe.
type WorkerStatusUpdate struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Endpoint  string `json:"endpoint,omitempty"`
	WorkDir   string `json:"work_dir,omitempty"`
	Token     string `json:"token,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Env       string `json:"env,omitempty"`
}
