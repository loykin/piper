package serving

type WorkerSyncStatusRequest struct {
	Services []WorkerSyncStatusTarget `json:"services"`
}

type WorkerSyncStatusTarget struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type WorkerSyncStatusResponse struct {
	Statuses map[string]string `json:"statuses"`
}

type WorkerStatusUpdate struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Endpoint string `json:"endpoint,omitempty"`
}
