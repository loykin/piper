package run

import (
	"time"

	"github.com/loykin/piper/internal/redact"
	"gopkg.in/yaml.v3"
)

const (
	StatusRunning   = "running"
	StatusScheduled = "scheduled"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// Step-level terminal statuses, as written by internal/queue/queue.go.
// A step not in this set (in practice: StepStatusRunning, or no row yet) is
// still "owned" work — see StepRepository.ListNonTerminalByWorker.
const (
	StepStatusRunning  = "running"
	StepStatusDone     = "done"
	StepStatusFailed   = "failed"
	StepStatusSkipped  = "skipped"
	StepStatusCanceled = "canceled"
)

type Run struct {
	ID              string     `json:"id"                        db:"id"`
	ProjectID       string     `json:"project_id"                db:"project_id"`
	ScheduleID      string     `json:"schedule_id,omitempty"     db:"schedule_id"`
	Experiment      string     `json:"experiment,omitempty"      db:"experiment"`
	PipelineName    string     `json:"pipeline_name"             db:"pipeline_name"`
	PipelineVersion int        `json:"pipeline_version,omitempty" db:"-"`
	Status          string     `json:"status"                    db:"status"`
	StartedAt       time.Time  `json:"started_at"                db:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"        db:"ended_at"`
	ScheduledAt     *time.Time `json:"scheduled_at,omitempty"    db:"scheduled_at"`
	PipelineYAML    string     `json:"pipeline_yaml,omitempty"   db:"pipeline_yaml"`
	ParamsJSON      string     `json:"params_json,omitempty"     db:"params_json"`
	CreatedBy       string     `json:"created_by,omitempty"      db:"created_by"`
	// WorkerID is the agent this run is bound to (see AGENTS.md's "Worker
	// Assignment": one run always executes on one worker). Not yet written by
	// any repository method in this package — reserved for the worker-side
	// scheduler's DB-access interface.
	WorkerID string `json:"worker_id,omitempty" db:"worker_id"`
	// WorkerLastSeenAt is the last time the bound worker reported this run as
	// still owned (see Repository.TouchWorkerLastSeen). Nil until the first
	// heartbeat lands. Used to detect a permanently-lost worker.
	WorkerLastSeenAt *time.Time `json:"worker_last_seen_at,omitempty" db:"worker_last_seen_at"`
	// CancelRequestedAt is set when a cancel was requested but couldn't be
	// relayed to the bound worker immediately (see
	// Repository.SetCancelRequested). Nil means no cancel is pending.
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty" db:"cancel_requested_at"`
}

// VersionFromYAML extracts metadata.version from the stored pipeline YAML.
// Returns 0 if absent or parsing fails.
func (r *Run) VersionFromYAML() int {
	var doc struct {
		Metadata struct {
			Version int `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(r.PipelineYAML), &doc); err != nil {
		return 0
	}
	return doc.Metadata.Version
}

type Step struct {
	ProjectID string     `json:"project_id"          db:"project_id"`
	RunID     string     `json:"run_id"              db:"run_id"`
	StepName  string     `json:"step_name"           db:"step_name"`
	Status    string     `json:"status"              db:"status"`
	StartedAt *time.Time `json:"started_at,omitempty" db:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"   db:"ended_at"`
	Error     string     `json:"error,omitempty"      db:"error"`
	Attempts  int        `json:"attempts"             db:"attempts"`
	// WorkerID is the agent this step is assigned to. See
	// StepRepository.ListNonTerminalByWorker.
	WorkerID string `json:"worker_id,omitempty" db:"worker_id"`
}

// Redact returns a copy of the Run with sensitive fields masked.
func (r *Run) Redact() *Run {
	if r == nil {
		return nil
	}
	cp := *r
	cp.PipelineYAML = redact.String(cp.PipelineYAML)
	cp.ParamsJSON = redact.String(cp.ParamsJSON)
	return &cp
}

type RunFilter struct {
	Experiment   string
	PipelineName string
	ScheduleID   string
	Status       string
	// MetricStep/MetricKey/MetricOrder enable metric-sorted listing.
	// Order is "asc" or "desc" (default "desc").
	MetricStep  string
	MetricKey   string
	MetricOrder string
	// Limit caps the number of rows returned. 0 = no limit (return everything
	// matching the filter). Offset is only meaningful when Limit > 0.
	Limit  int
	Offset int
}
