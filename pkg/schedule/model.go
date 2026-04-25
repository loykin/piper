package schedule

import "time"

type Schedule struct {
	ID           string     `json:"id"                    db:"id"`
	Name         string     `json:"name"                  db:"name"`
	OwnerID      string     `json:"owner_id,omitempty"    db:"owner_id"`
	PipelineYAML string     `json:"pipeline_yaml"         db:"pipeline_yaml"`
	ScheduleType string     `json:"schedule_type"         db:"schedule_type"`
	CronExpr     string     `json:"cron_expr,omitempty"   db:"cron_expr"`
	Enabled      bool       `json:"enabled"               db:"enabled"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty" db:"last_run_at"`
	NextRunAt    time.Time  `json:"next_run_at"           db:"next_run_at"`
	ParamsJSON   string     `json:"params_json,omitempty" db:"params_json"`
	CreatedAt    time.Time  `json:"created_at"            db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"            db:"updated_at"`
}
