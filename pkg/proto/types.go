package proto

import "time"

// BuiltinVars holds system-injected variables that are propagated to every step.
// Add new scheduled/contextual metadata here — each field becomes a PIPER_* env var.
type BuiltinVars struct {
	// ScheduledAt is the logical/scheduled execution time (Airflow-style execution_date).
	// Set by cron or external schedulers. Nil means an ad-hoc/manual run.
	// Exposed as PIPER_SCHEDULED_AT (RFC3339 UTC).
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

// Task is the unit of work the server delivers to a worker
type Task struct {
	ID        string      `json:"id"`
	RunID     string      `json:"run_id"`
	StepName  string      `json:"step_name"`
	Step      []byte      `json:"step"`     // pipeline.Step JSON
	Pipeline  []byte      `json:"pipeline"` // pipeline.Pipeline JSON
	WorkDir   string      `json:"work_dir"`
	OutputDir string      `json:"output_dir"`
	CreatedAt time.Time   `json:"created_at"`
	Label     string      `json:"label"` // worker label that should handle this task
	Vars      BuiltinVars `json:"vars,omitempty"`
}

// TaskResult is the result a worker reports back to the server
type TaskResult struct {
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"` // done | failed
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Attempts  int       `json:"attempts"`
}

// RunRequest is used by a client to request a pipeline run
type RunRequest struct {
	PipelineYAML string         `json:"pipeline_yaml"`
	Params       map[string]any `json:"params,omitempty"`
	WorkDir      string         `json:"work_dir,omitempty"`
	OutputDir    string         `json:"output_dir,omitempty"`
	Vars         BuiltinVars    `json:"vars,omitempty"`
}

// RunResponse is the response to a run request
type RunResponse struct {
	RunID string `json:"run_id"`
}
