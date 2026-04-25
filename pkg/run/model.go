package run

import "time"

const (
	StatusRunning   = "running"
	StatusScheduled = "scheduled"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
)

type Run struct {
	ID           string     `json:"id"`
	ScheduleID   string     `json:"schedule_id,omitempty"`
	OwnerID      string     `json:"owner_id,omitempty"`
	PipelineName string     `json:"pipeline_name"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	ScheduledAt  *time.Time `json:"scheduled_at,omitempty"`
	PipelineYAML string     `json:"pipeline_yaml,omitempty"`
}

type Step struct {
	RunID     string     `json:"run_id"`
	StepName  string     `json:"step_name"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	Attempts  int        `json:"attempts"`
}

type RunFilter struct {
	OwnerID      string
	PipelineName string
	ScheduleID   string
	Status       string
}
