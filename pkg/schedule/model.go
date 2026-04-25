package schedule

import "time"

type Schedule struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	OwnerID      string     `json:"owner_id,omitempty"`
	PipelineYAML string     `json:"pipeline_yaml"`
	ScheduleType string     `json:"schedule_type"`
	CronExpr     string     `json:"cron_expr,omitempty"`
	Enabled      bool       `json:"enabled"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
	NextRunAt    time.Time  `json:"next_run_at"`
	ParamsJSON   string     `json:"params_json,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
