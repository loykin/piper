package memberclient

import (
	"time"

	"github.com/loykin/piper/internal/proto"
)

// RunSummary mirrors run.Run's JSON shape field-for-field (already redacted,
// with PipelineVersion populated) so the external HTTP contract is
// unchanged. See the package doc for why this isn't run.Run itself.
type RunSummary struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"project_id"`
	ScheduleID      string     `json:"schedule_id,omitempty"`
	Experiment      string     `json:"experiment,omitempty"`
	PipelineName    string     `json:"pipeline_name"`
	PipelineVersion int        `json:"pipeline_version,omitempty"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	ScheduledAt     *time.Time `json:"scheduled_at,omitempty"`
	PipelineYAML    string     `json:"pipeline_yaml,omitempty"`
	ParamsJSON      string     `json:"params_json,omitempty"`
	CreatedBy       string     `json:"created_by,omitempty"`
}

// StepSummary mirrors run.Step's JSON shape field-for-field.
type StepSummary struct {
	ProjectID string     `json:"project_id"`
	RunID     string     `json:"run_id"`
	StepName  string     `json:"step_name"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	Attempts  int        `json:"attempts"`
}

// RunDetail is GetRun's result. The Handler decides the HTTP JSON shape
// ({"run":..., "steps":...}) — Member just returns the data.
type RunDetail struct {
	Run   RunSummary
	Steps []StepSummary
}

type SubmitRunRequest struct {
	IdempotencyKey string
	YAML           string
	Params         map[string]any
	Experiment     string
	Vars           proto.BuiltinVars
}

type SubmitRunResponse struct {
	RunID string
}

// SweepTrial mirrors run.SweepTrial's JSON shape.
type SweepTrial struct {
	Params map[string]any `json:"params"`
}

type SubmitSweepRequest struct {
	YAML       string
	Experiment string
	Runs       []SweepTrial
}

type SubmitSweepResponse struct {
	Experiment string
	RunIDs     []string
}

// ListRunsRequest mirrors run.RunFilter's fields plus the list-only
// IncludeSteps flag (run.Handler's include_steps query param).
type ListRunsRequest struct {
	Experiment   string
	PipelineName string
	ScheduleID   string
	Status       string
	MetricStep   string
	MetricKey    string
	MetricOrder  string
	Limit        int
	Offset       int
	IncludeSteps bool
}

type ListRunsResponse struct {
	Runs []RunSummary
	// Steps is populated only when the request set IncludeSteps, keyed by run ID.
	Steps map[string][]StepSummary
	// Total is valid only when the request set Limit > 0 (matches the
	// existing X-Total-Count header semantics).
	Total int
}
