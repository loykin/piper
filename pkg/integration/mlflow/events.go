package mlflow

import "time"

// Outbox event types this package's Exporter understands (design doc
// section 7.1/7.4). Only the Phase 1 pipeline run lifecycle events are
// defined here — metric/artifact snapshot events (section 7.3/8) and
// notebook execution events (section 7.5) are later phases.
const (
	EventTypePipelineRunCreated  = "pipeline_run.created"
	EventTypePipelineRunFinished = "pipeline_run.finished"
)

// PipelineRunCreatedPayload is outbox.Event.PayloadJSON's shape for
// EventTypePipelineRunCreated (design doc section 7.1's field list).
type PipelineRunCreatedPayload struct {
	ProjectID       string         `json:"project_id"`
	RunID           string         `json:"run_id"`
	PipelineName    string         `json:"pipeline_name"`
	PipelineVersion int            `json:"pipeline_version,omitempty"`
	Experiment      string         `json:"experiment,omitempty"`
	Params          map[string]any `json:"params,omitempty"`
	CreatedBy       string         `json:"created_by,omitempty"`
	RuntimeType     string         `json:"runtime_type,omitempty"`
	// StartTime is the run's actual/started time for an immediately
	// dispatched run, or its future ScheduledAt time for a scheduled run
	// (design doc section 7.1 payload: "start/scheduled time" — a
	// scheduled run is durably enqueued at creation time too, carrying its
	// scheduled time; see enqueue.go's doc comment for why both StartRun
	// call sites enqueue this event).
	StartTime time.Time `json:"start_time"`
	// RunURL lets the exporter set the piper.url tag (design doc section
	// 7.2). Piper has no configured public base URL today (see enqueue.go),
	// so this is a relative API path, not an absolute URL.
	RunURL string `json:"run_url,omitempty"`
}

// PipelineRunFinishedPayload is outbox.Event.PayloadJSON's shape for
// EventTypePipelineRunFinished (design doc section 7.4).
type PipelineRunFinishedPayload struct {
	ProjectID string    `json:"project_id"`
	RunID     string    `json:"run_id"`
	Status    string    `json:"status"` // Piper run.Status* value
	EndTime   time.Time `json:"end_time"`
}
