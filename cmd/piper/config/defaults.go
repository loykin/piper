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
		NotebookExecution: NotebookExecutionConfig{
			MCPPolicy: "approval_required", MaxRunningPerNotebook: 1,
			MaxKernelsPerNotebook: 2, MaxQueuedPerProject: 20,
			KernelIdleTTL: 30 * time.Minute, CellTimeout: 5 * time.Minute,
			ExecutionTimeout: time.Hour, InlineOutputBytes: 65536, FileReadBytes: 1048576,
		},
		Integrations: IntegrationsConfig{MLflow: MLflowConfig{
			DispatcherConcurrency: 2, BatchSize: 100, RequestTimeout: 10 * time.Second,
			MaxAttemptsBeforeDead: 20, LeaseDuration: 30 * time.Second, PollInterval: 5 * time.Second,
		}},
		MCP: MCPConfig{SessionTTL: 30 * time.Minute},
	}
}
