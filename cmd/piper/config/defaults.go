package config

import "time"

func Defaults() RootConfig {
	return RootConfig{
		Version: 4,
		Log:     LogConfig{Format: "text", Level: "info"},
		Server: ServerConfig{
			HTTPAddr: ":8080",
			DB:       DBConfig{Driver: "sqlite"},
			DataDir:  "./piper-outputs",
			Schedule: ScheduleConfig{MisfirePolicy: "run_once", MisfireGracePeriod: 5 * time.Minute},
			Local: LocalConfig{
				Pipeline: true, Notebook: true, Serving: true, Concurrency: 4,
				NotebookCfg: LocalNotebookConfig{NotebooksRoot: "./notebooks", PortRange: "8888-9900"},
			},
		},
		Worker: WorkerConfig{StateDir: "./piper-worker-state"},
	}
}
