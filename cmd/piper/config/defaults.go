package config

import "time"

func Defaults() RootConfig {
	return RootConfig{
		Version: 1,
		Log:     LogConfig{Format: "text", Level: "info"},
		Server: ServerConfig{
			HTTPAddr:  ":8080",
			AgentAddr: ":9090",
			DB:        DBConfig{Driver: "sqlite"},
			Run:       RunConfig{OutputDir: "./piper-outputs", Retries: 2, RetryDelay: 5 * time.Second, Concurrency: 4},
			Schedule:  ScheduleConfig{MisfirePolicy: "skip", MisfireGracePeriod: time.Minute},
			Serving:   ServerServing{Delegate: true},
			Notebook:  ServerNotebook{Delegate: true, K8s: K8sNotebookConfig{Image: "jupyter/minimal-notebook:latest", StorageSize: "10Gi"}},
			Local:     LocalConfig{Pipeline: true, Notebook: true, Serving: true},
		},
		Workers: WorkersConfig{
			Pipeline: PipelineWorkerConfig{Runtime: "baremetal", Concurrency: 4, OutputDir: "./piper-outputs", Docker: PipelineDockerConfig{Network: "bridge"}},
			Notebook: NotebookWorkerConfig{Mode: "process", NotebooksRoot: "./notebooks", PortRange: "8888-9900", Docker: NotebookDockerConfig{Image: "jupyter/minimal-notebook:latest", Network: "bridge"}},
			Serving:  ServingWorkerConfig{Mode: "process", Docker: ServingDockerConfig{Network: "bridge"}},
			K8s:      K8sWorkerConfig{Enabled: []string{"pipeline", "notebook", "serving"}, InCluster: true, Pipeline: K8sPipelineConfig{WorkerImage: "piper/piper:latest", AgentImagePullPolicy: "IfNotPresent"}, Notebook: K8sNotebookConfig{Image: "jupyter/minimal-notebook:latest", StorageSize: "10Gi"}},
		},
	}
}
