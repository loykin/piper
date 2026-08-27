package config

import "time"

func Defaults() RootConfig {
	return RootConfig{
		Version: 4,
		Log:     LogConfig{Format: "text", Level: "info"},
		Stats: StatsConfig{
			Spool:   StatsSpoolConfig{MaxBytes: 1 << 30},
			Logs:    StatsBackendConfig{ManageRetention: true},
			Metrics: StatsBackendConfig{ManageRetention: true},
		},
		Server: ServerConfig{
			HTTPAddr: ":8080",
			DB:       DBConfig{Driver: "sqlite"},
			DataDir:  "./piper-outputs",
			Schedule: ScheduleConfig{MisfirePolicy: "run_once", MisfireGracePeriod: 5 * time.Minute},
		},
		Notebook: NotebookConfig{NotebooksRoot: "./notebooks", PortRange: "8888-9900"},
	}
}
