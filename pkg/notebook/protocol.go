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

// FSUploadSnapshotRequest asks the volume-owning worker to upload selected
// paths directly to artifact storage. Source bytes never pass through the
// master control plane.
type FSUploadSnapshotRequest struct {
	ProjectID    string   `json:"project_id"`
	VolumeID     string   `json:"volume_id"`
	WorkDir      string   `json:"work_dir,omitempty"`
	Notebook     string   `json:"notebook,omitempty"`
	Token        string   `json:"token,omitempty"`
	SnapshotID   string   `json:"snapshot_id"`
	Paths        []string `json:"paths"`
	StorageURL   string   `json:"storage_url"`
	StorageToken string   `json:"storage_token,omitempty"`
}

type FSUploadSnapshotResponse struct {
	Files int `json:"files"`
}
