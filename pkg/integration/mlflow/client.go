package mlflow

import (
	"context"
	"errors"
	"io"
)

// ErrNotImplemented is returned by the stub Client until the follow-up
// exporter task implements the real MLflow Tracking REST calls (design doc
// section 12). Wiring this up further (auth, SSRF-safe HTTP client reusing
// pkg/notify/http.go's safeHTTPClient pattern, request/response mapping,
// retry policy per design doc section 10.2) is explicitly out of scope for
// this foundation phase.
var ErrNotImplemented = errors.New("mlflow: not implemented")

// Client wraps the minimal subset of the official MLflow Tracking REST API
// the exporter needs (design doc section 12). It intentionally does not
// expose the full API surface, does not shell out to the MLflow Python SDK,
// and does not run a sidecar — Piper stays a single Go binary.
type Client interface {
	GetExperimentByName(context.Context, string) (*Experiment, error)
	CreateExperiment(context.Context, CreateExperimentRequest) (*Experiment, error)
	CreateRun(context.Context, CreateRunRequest) (*Run, error)
	GetRun(context.Context, string) (*Run, error)
	SearchRuns(context.Context, SearchRunsRequest) (RunPage, error)
	LogBatch(context.Context, LogBatchRequest) error
	UpdateRun(context.Context, UpdateRunRequest) error
	UploadArtifact(context.Context, string, string, io.Reader, int64) error
}

// Experiment mirrors the fields of MLflow's Experiment resource that this
// adapter needs. See
// https://mlflow.org/docs/latest/api_reference/rest-api.html#experiment.
type Experiment struct {
	ExperimentID     string
	Name             string
	ArtifactLocation string
	LifecycleStage   string
	Tags             map[string]string
}

// CreateExperimentRequest is the input to Client.CreateExperiment.
type CreateExperimentRequest struct {
	Name             string
	ArtifactLocation string
	Tags             map[string]string
}

// RunStatus is the MLflow run lifecycle status (design doc section 7.4's
// Piper -> MLflow status mapping table).
type RunStatus string

const (
	RunStatusRunning  RunStatus = "RUNNING"
	RunStatusFailed   RunStatus = "FAILED"
	RunStatusFinished RunStatus = "FINISHED"
	RunStatusKilled   RunStatus = "KILLED"
)

// PiperRunStatusToMLflow maps a Piper run terminal status to the MLflow run
// status it should be reported as (design doc section 7.4). It returns
// ("", false) for a Piper status this table does not define.
func PiperRunStatusToMLflow(piperStatus string) (RunStatus, bool) {
	switch piperStatus {
	case "running":
		return RunStatusRunning, true
	case "success":
		return RunStatusFinished, true
	case "failed":
		return RunStatusFailed, true
	case "canceled":
		return RunStatusKilled, true
	default:
		return "", false
	}
}

// Run mirrors the fields of MLflow's Run resource (run info + a thin view
// of run data) that this adapter needs.
type Run struct {
	RunID          string
	ExperimentID   string
	Status         RunStatus
	StartTime      int64 // milliseconds since epoch, per MLflow's wire format
	EndTime        int64 // 0 if not yet terminal
	ArtifactURI    string
	LifecycleStage string
	Params         map[string]string
	Tags           map[string]string
}

// CreateRunRequest is the input to Client.CreateRun.
type CreateRunRequest struct {
	ExperimentID string
	RunName      string
	StartTime    int64
	Tags         map[string]string
}

// SearchRunsRequest is the input to Client.SearchRuns. Filter uses MLflow's
// own search-expression syntax (e.g. `tags.piper.run_id = '...'`) — see
// design doc section 7.1's use of this to resolve an ambiguous
// create-run timeout via `tags.piper.run_id`/`tags.piper.integration_id`
// search.
type SearchRunsRequest struct {
	ExperimentIDs []string
	Filter        string
	MaxResults    int
	PageToken     string
}

// RunPage is one page of Client.SearchRuns results.
type RunPage struct {
	Runs          []*Run
	NextPageToken string
}

// Metric is a single MLflow metric point (design doc section 7.3).
type Metric struct {
	Key       string
	Value     float64
	Timestamp int64 // milliseconds since epoch
	Step      int64
}

// Param is a single MLflow run parameter (design doc section 7.2). MLflow
// rejects re-logging the same key with a different value in one run, so
// callers only send the create-time snapshot once.
type Param struct {
	Key   string
	Value string
}

// Tag is a single MLflow run tag.
type Tag struct {
	Key   string
	Value string
}

// LogBatchRequest is the input to Client.LogBatch, mirroring MLflow's
// log-batch endpoint. batch_size must stay within the server-configured
// `integrations.mlflow.batch_size` limit (design doc section 13) — enforcing
// that is the future dispatcher's job, not this client.
type LogBatchRequest struct {
	RunID   string
	Metrics []Metric
	Params  []Param
	Tags    []Tag
}

// UpdateRunRequest is the input to Client.UpdateRun, used both for the
// mid-run tag reconciliation (design doc section 4.2) and the terminal
// status update (section 7.4).
type UpdateRunRequest struct {
	RunID   string
	Status  RunStatus
	EndTime int64
	RunName string
}

// stubClient is a placeholder Client that returns ErrNotImplemented for
// every operation. It exists so callers in this phase (repository/model
// tests, future dependency wiring) have something satisfying the Client
// interface without a real MLflow Tracking Server; the actual HTTP
// implementation is a follow-up task (design doc section 12's client.go /
// exporter.go).
type stubClient struct{}

// NewStubClient returns a Client whose every method returns
// ErrNotImplemented. It deliberately does not take a TrackingURI or
// credential — this phase only needs the shape of the dependency, not a
// working one.
func NewStubClient() Client {
	return stubClient{}
}

func (stubClient) GetExperimentByName(context.Context, string) (*Experiment, error) {
	return nil, ErrNotImplemented
}

func (stubClient) CreateExperiment(context.Context, CreateExperimentRequest) (*Experiment, error) {
	return nil, ErrNotImplemented
}

func (stubClient) CreateRun(context.Context, CreateRunRequest) (*Run, error) {
	return nil, ErrNotImplemented
}

func (stubClient) GetRun(context.Context, string) (*Run, error) {
	return nil, ErrNotImplemented
}

func (stubClient) SearchRuns(context.Context, SearchRunsRequest) (RunPage, error) {
	return RunPage{}, ErrNotImplemented
}

func (stubClient) LogBatch(context.Context, LogBatchRequest) error {
	return ErrNotImplemented
}

func (stubClient) UpdateRun(context.Context, UpdateRunRequest) error {
	return ErrNotImplemented
}

func (stubClient) UploadArtifact(context.Context, string, string, io.Reader, int64) error {
	return ErrNotImplemented
}
