package config

import "time"

func Defaults() RootConfig {
	return RootConfig{
		Version: 2,
		Log:     LogConfig{Format: "text", Level: "info"},
		Server: ServerConfig{
			HTTPAddr: ":8080",
			DB:       DBConfig{Driver: "sqlite"},
			DataDir:  "./piper-outputs",
			Schedule: ScheduleConfig{MisfirePolicy: "skip", MisfireGracePeriod: time.Minute},
			Local:    LocalConfig{Pipeline: true, Notebook: true, Serving: true},
		},
		Workers: WorkersConfig{
			Common:   WorkerCommonConfig{StateDir: "./piper-worker-state"},
			Pipeline: PipelineWorkerConfig{Runtime: "baremetal", Concurrency: 4, OutputDir: "./piper-outputs", Docker: PipelineDockerConfig{Network: "bridge"}},
			Notebook: NotebookWorkerConfig{Mode: "process", NotebooksRoot: "./notebooks", PortRange: "8888-9900", Docker: NotebookDockerConfig{Network: "bridge"}},
			Serving:  ServingWorkerConfig{Mode: "process", Docker: ServingDockerConfig{Network: "bridge"}},
			K8s:      K8sWorkerConfig{Enabled: []string{"pipeline", "notebook", "serving"}, InCluster: true, Pipeline: K8sPipelineConfig{RunnerImage: "piper/piper:latest", RunnerImagePullPolicy: "IfNotPresent"}},
		},
	}
}
