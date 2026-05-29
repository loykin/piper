package run

import (
	"time"

	"github.com/piper/piper/pkg/secret"
)

const (
	StatusRunning   = "running"
	StatusScheduled = "scheduled"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

type Run struct {
	ID           string     `json:"id"                     db:"id"`
	ScheduleID   string     `json:"schedule_id,omitempty"  db:"schedule_id"`
	OwnerID      string     `json:"owner_id,omitempty"     db:"owner_id"`
	Experiment   string     `json:"experiment,omitempty"   db:"experiment"`
	PipelineName string     `json:"pipeline_name"          db:"pipeline_name"`
	Status       string     `json:"status"                 db:"status"`
	StartedAt    time.Time  `json:"started_at"             db:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"     db:"ended_at"`
	ScheduledAt  *time.Time `json:"scheduled_at,omitempty" db:"scheduled_at"`
	PipelineYAML string     `json:"pipeline_yaml,omitempty" db:"pipeline_yaml"`
	ParamsJSON   string     `json:"params_json,omitempty" db:"params_json"`
}

type Step struct {
	RunID     string     `json:"run_id"              db:"run_id"`
	StepName  string     `json:"step_name"           db:"step_name"`
	Status    string     `json:"status"              db:"status"`
	StartedAt *time.Time `json:"started_at,omitempty" db:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"   db:"ended_at"`
	Error     string     `json:"error,omitempty"      db:"error"`
	Attempts  int        `json:"attempts"             db:"attempts"`
}

// Redact returns a copy of the Run with sensitive fields masked.
func (r *Run) Redact() *Run {
	if r == nil {
		return nil
	}
	cp := *r
	cp.PipelineYAML = secret.RedactString(cp.PipelineYAML)
	cp.ParamsJSON = secret.RedactString(cp.ParamsJSON)
	return &cp
}

type RunFilter struct {
	OwnerID      string
	Experiment   string
	PipelineName string
	ScheduleID   string
	Status       string
}
