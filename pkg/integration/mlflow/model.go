// Package mlflow implements the connection/credential/schema foundation for
// exporting Piper Pipeline runs (and, in a later phase, Notebook executions)
// to an external MLflow Tracking Server. See docs/mlflow-tracking-adapter.md
// (design doc) section 5 (connection/credential), 6 (ID mapping), and 12
// (package boundaries) for the full design.
//
// This file only defines the domain model. The exporter, dispatcher, outbox
// delivery logic, REST handlers, and UI are out of scope for this phase —
// see repository.go for the (interface-only) persistence contract and
// client.go for the (skeleton) MLflow Tracking REST client.
package mlflow

import "time"

// ArtifactMode selects how Piper artifacts are represented in MLflow. v1
// only supports Reference; MirrorSelected/MirrorAll are reserved for a
// later phase (design doc section 8).
type ArtifactMode string

const (
	// ArtifactModeReference is the only mode supported in v1: Piper artifact
	// store stays authoritative and MLflow only receives a small JSON
	// manifest + link (design doc section 8.1).
	ArtifactModeReference ArtifactMode = "reference"
	// ArtifactModeMirrorSelected is reserved for a future phase (design doc
	// section 8.3) — copying explicitly selected artifacts into MLflow's own
	// artifact store for Model Registry registration.
	ArtifactModeMirrorSelected ArtifactMode = "mirror_selected"
	// ArtifactModeMirrorAll is reserved and intentionally never implemented
	// as a default (design doc section 8.2 explains why mirroring
	// everything is rejected).
	ArtifactModeMirrorAll ArtifactMode = "mirror_all"
)

// SourceType identifies what kind of Piper resource an MLflowRunLink maps to.
type SourceType string

const (
	SourceTypePipeline          SourceType = "pipeline"
	SourceTypeNotebookExecution SourceType = "notebook_execution"
)

// SyncStatus is the reconciliation state of a single MLflowRunLink (design
// doc section 6.2).
type SyncStatus string

const (
	SyncStatusPending  SyncStatus = "pending"
	SyncStatusSyncing  SyncStatus = "syncing"
	SyncStatusSynced   SyncStatus = "synced"
	SyncStatusDegraded SyncStatus = "degraded"
	SyncStatusDisabled SyncStatus = "disabled"
)

// MLflowIntegration is a project-scoped connection to an external MLflow
// Tracking Server (design doc section 5.1). Connections are DB resources,
// not server YAML config — a project admin creates them at runtime.
//
// v1 allows at most one Default=true integration per project; repositories
// must enforce this (see repository.go's Repository.CreateIntegration /
// UpdateIntegration doc comments).
type MLflowIntegration struct {
	ID                       string    `json:"id"                          db:"id"`
	ProjectID                string    `json:"project_id"                  db:"project_id"`
	Name                     string    `json:"name"                        db:"name"`
	TrackingURI              string    `json:"tracking_uri"                db:"tracking_uri"`
	CredentialRef            string    `json:"credential_ref"              db:"credential_ref"`
	Enabled                  bool      `json:"enabled"                     db:"enabled"`
	Default                  bool      `json:"default"                     db:"is_default"`
	ExportPipelines          bool      `json:"export_pipelines"            db:"export_pipelines"`
	ExportNotebookExecutions bool      `json:"export_notebook_executions"  db:"export_notebook_executions"`
	ExperimentTemplate       string    `json:"experiment_template"         db:"experiment_template"`
	ArtifactMode             string    `json:"artifact_mode"               db:"artifact_mode"`
	CreatedBy                string    `json:"created_by,omitempty"        db:"created_by"`
	CreatedAt                time.Time `json:"created_at"                  db:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"                  db:"updated_at"`
}

// DefaultExperimentTemplate is the template used when ExperimentTemplate is
// empty (design doc section 5.1): "piper/{project_id}/{experiment_or_pipeline}".
const DefaultExperimentTemplate = "piper/{project_id}/{experiment_or_pipeline}"

// Validate checks the structural invariants this package is responsible for
// at write time (design doc section 5.3's SSRF boundary plus the basic
// required-field/enum checks). It does not check credential existence/kind —
// that requires the credential.Store dependency the future service layer
// will inject; this is the "at minimum, validate at model/repository write
// time" floor called out for this phase.
func (m *MLflowIntegration) Validate(policy SSRFPolicy) error {
	return validateIntegration(m, policy)
}

// MLflowExperimentLink maps a Piper "group" (an Experiment name or, absent
// that, a pipeline name) to an MLflow experiment, scoped to one integration
// (design doc section 6.1). Unique key: (IntegrationID, ProjectID,
// PiperGroupKey).
type MLflowExperimentLink struct {
	IntegrationID      string    `json:"integration_id"        db:"integration_id"`
	ProjectID          string    `json:"project_id"            db:"project_id"`
	PiperGroupKey      string    `json:"piper_group_key"       db:"piper_group_key"`
	MLflowExperimentID string    `json:"mlflow_experiment_id"  db:"mlflow_experiment_id"`
	MLflowName         string    `json:"mlflow_name"           db:"mlflow_name"`
	CreatedAt          time.Time `json:"created_at"            db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"            db:"updated_at"`
}

// MLflowRunLink maps a single Piper pipeline run or notebook execution to a
// single MLflow run, scoped to one integration (design doc section 6.2).
// Unique key: (IntegrationID, ProjectID, SourceType, SourceID).
type MLflowRunLink struct {
	IntegrationID      string     `json:"integration_id"        db:"integration_id"`
	ProjectID          string     `json:"project_id"            db:"project_id"`
	SourceType         string     `json:"source_type"           db:"source_type"`
	SourceID           string     `json:"source_id"             db:"source_id"`
	MLflowExperimentID string     `json:"mlflow_experiment_id"  db:"mlflow_experiment_id"`
	MLflowRunID        string     `json:"mlflow_run_id"         db:"mlflow_run_id"`
	MLflowRunURL       string     `json:"mlflow_run_url"        db:"mlflow_run_url"`
	SyncStatus         string     `json:"sync_status"           db:"sync_status"`
	LastSequence       int64      `json:"last_sequence"         db:"last_sequence"`
	LastErrorCode      string     `json:"last_error_code,omitempty"    db:"last_error_code"`
	LastErrorMessage   string     `json:"last_error_message,omitempty" db:"last_error_message"`
	LastSyncedAt       *time.Time `json:"last_synced_at,omitempty"     db:"last_synced_at"`
	CreatedAt          time.Time  `json:"created_at"            db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"            db:"updated_at"`
}
