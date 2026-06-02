package notebook

type WorkerProvisionVolumeRequest struct {
	VolumeID    string `json:"volume_id"`
	StorageSize string `json:"storage_size,omitempty"`
}

type WorkerProvisionVolumeResponse struct {
	WorkDir string `json:"work_dir"`
}

type WorkerStartRequest struct {
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
	Name string `json:"name"`
}

type WorkerDeprovisionVolumeRequest struct {
	VolumeID string `json:"volume_id"`
}

type WorkerSyncStatusRequest struct {
	Names []string `json:"names"`
}

type WorkerSyncStatusResponse struct {
	Statuses map[string]string `json:"statuses"`
}
