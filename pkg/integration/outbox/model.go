// Package outbox implements the generic durable "integration outbox"
// pattern described in docs/mlflow-tracking-adapter.md section 6.3: a
// durable, ordered, at-least-once delivery queue that sits between a Piper
// domain lifecycle event (a pipeline run starting, finishing, ...) and an
// external integration adapter (MLflow today; potentially other adapters
// later). This package is deliberately integration-agnostic — it knows
// nothing about MLflow, experiments, or runs. pkg/integration/mlflow's
// Exporter is the MLflow-specific outbox.Handler implementation that gives
// EventType/PayloadJSON their meaning.
//
// Why a separate table/queue instead of internal/event.Bus: event.Bus is an
// in-memory, best-effort notification bus that drops events for slow
// subscribers and does not survive a restart. An external integration sync
// needs a durable, replayable, ordered record that survives process
// restarts and MLflow outages — see design doc section 4.3 (failure
// isolation) and section 6.3.
package outbox

import "time"

// Status is the lifecycle state of a single outbox event.
type Status string

const (
	// StatusPending is claimable by a dispatcher.
	StatusPending Status = "pending"
	// StatusDelivering is currently claimed by a dispatcher (lease held).
	StatusDelivering Status = "delivering"
	// StatusDelivered is a terminal success state.
	StatusDelivered Status = "delivered"
	// StatusDead is a terminal failure state — either a non-retryable error
	// or attempts exhausted (design doc section 10.5). Dead events are not
	// automatically retried; they require an explicit resync (out of scope
	// for this phase — see design doc section 10.5's MLflowSyncJob).
	StatusDead Status = "dead"
	// StatusDisabled holds events belonging to an integration that has been
	// deleted/disabled (design doc section 11.1: "dispatcher 중지, pending
	// outbox를 disabled 상태로 보존"). Not part of the design doc's literal
	// section 6.3 enum (pending | delivering | delivered | dead) — added
	// here as the deliberate, documented resolution of that gap: section
	// 11.1 explicitly requires a "disabled" outbox state on integration
	// delete, so this package adds it as a fifth terminal-ish state (an
	// admin action re-enabling the integration is the intended path back to
	// pending; that re-activation endpoint is out of scope for this phase,
	// same as the reconciler).
	StatusDisabled Status = "disabled"
)

// Aggregate type constants. An aggregate is the Piper resource whose
// lifecycle events are exported in order (design doc section 6.3's
// "aggregate별 sequence ordering").
const (
	AggregateTypePipelineRun       = "pipeline_run"
	AggregateTypeNotebookExecution = "notebook_execution"
)

// Event is a single durable outbox row (design doc section 6.3's
// IntegrationOutboxEvent). PayloadJSON is a bounded snapshot the consuming
// Handler decodes according to EventType — this package never inspects it.
type Event struct {
	ID             string     `json:"id"                          db:"id"`
	IntegrationID  string     `json:"integration_id"               db:"integration_id"`
	ProjectID      string     `json:"project_id"                   db:"project_id"`
	AggregateType  string     `json:"aggregate_type"               db:"aggregate_type"`
	AggregateID    string     `json:"aggregate_id"                 db:"aggregate_id"`
	Sequence       int64      `json:"sequence"                     db:"sequence"`
	EventType      string     `json:"event_type"                   db:"event_type"`
	PayloadJSON    []byte     `json:"payload_json"                 db:"payload_json"`
	Status         string     `json:"status"                       db:"status"`
	Attempts       int        `json:"attempts"                     db:"attempts"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"              db:"next_attempt_at"`
	LeaseOwner     string     `json:"lease_owner,omitempty"        db:"lease_owner"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"   db:"lease_expires_at"`
	LastErrorCode  string     `json:"last_error_code,omitempty"    db:"last_error_code"`
	LastError      string     `json:"last_error,omitempty"         db:"last_error"`
	CreatedAt      time.Time  `json:"created_at"                   db:"created_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"       db:"delivered_at"`
}
