package mlflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/loykin/piper/pkg/integration/outbox"
)

// ClientFactory builds (or returns a cached) Client for one
// MLflowIntegration — resolving TrackingURI/credential is integration-
// specific and requires a credential.Store, which this package
// deliberately does not depend on directly (same DI pattern as e.g.
// runlifecycle.Deps.DeployService): the caller (piper.go) closes over its
// own credential.Store and constructs a real *NewHTTPClient per call.
// Implementations should be reasonably cheap to call repeatedly (the
// Exporter does not cache across Handle calls) but are not on any
// synchronous run-lifecycle path — only the async dispatcher calls this.
type ClientFactory func(ctx context.Context, integration *MLflowIntegration) (Client, error)

// Exporter is the MLflow-specific outbox.Handler: it decodes
// PipelineRunCreatedPayload/PipelineRunFinishedPayload events, resolves or
// creates the MLflow experiment/run mapping, and applies params/tags/status
// (design doc section 7.1, 7.2, 7.4). Metric/artifact export (section 7.3,
// 8) is out of scope for this phase.
type Exporter struct {
	Repo    Repository
	Clients ClientFactory
	// Now is overridable for tests.
	Now func() time.Time
}

// NewExporter constructs an Exporter with sane defaults.
func NewExporter(repo Repository, clients ClientFactory) *Exporter {
	return &Exporter{Repo: repo, Clients: clients, Now: time.Now}
}

var _ outbox.Handler = (*Exporter)(nil)

// Handle implements outbox.Handler.
func (e *Exporter) Handle(ctx context.Context, ev *outbox.Event) outbox.Outcome {
	now := e.now()

	integration, err := e.Repo.GetIntegration(ctx, ev.ProjectID, ev.IntegrationID)
	if err != nil {
		return retryOutcome(err, "integration_lookup_failed")
	}
	if integration == nil || integration.IsDeleted() {
		// The integration was deleted after this event was enqueued (and,
		// per DisableIntegrationEvents, most such events are already
		// StatusDisabled — this is the narrow race where deletion happens
		// between claim and Handle). Treat like a disabled integration
		// below: park it, don't dead-letter it.
		return outbox.Outcome{Retryable: true, RetryAfter: 5 * time.Minute, ErrorCode: "integration_missing", ErrorMessage: "mlflow integration no longer exists"}
	}
	if !integration.Enabled {
		return outbox.Outcome{Retryable: true, RetryAfter: 5 * time.Minute, ErrorCode: "integration_disabled", ErrorMessage: "mlflow integration is disabled"}
	}

	client, err := e.Clients(ctx, integration)
	if err != nil {
		return outbox.Outcome{Retryable: true, ErrorCode: "client_unavailable", ErrorMessage: redactErr(err)}
	}

	switch ev.EventType {
	case EventTypePipelineRunCreated:
		return e.handlePipelineRunCreated(ctx, integration, client, ev, now)
	case EventTypePipelineRunFinished:
		return e.handlePipelineRunFinished(ctx, integration, client, ev, now)
	default:
		// An event type this build of the Exporter doesn't understand
		// (e.g. a metric/artifact event enqueued by a future phase against
		// an older Exporter during a rolling deploy). Not retryable — no
		// amount of waiting makes this Exporter understand it.
		return outbox.Outcome{Retryable: false, ErrorCode: "unknown_event_type", ErrorMessage: "unrecognized outbox event_type " + ev.EventType}
	}
}

func (e *Exporter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// handlePipelineRunCreated implements design doc section 7.1's adapter
// steps 1-6 (integration enabled check already done by Handle above).
func (e *Exporter) handlePipelineRunCreated(ctx context.Context, integration *MLflowIntegration, client Client, ev *outbox.Event, now time.Time) outbox.Outcome {
	var payload PipelineRunCreatedPayload
	if err := json.Unmarshal(ev.PayloadJSON, &payload); err != nil {
		return outbox.Outcome{Retryable: false, ErrorCode: "invalid_payload", ErrorMessage: "malformed pipeline_run.created payload"}
	}

	link, outcome := e.ensureRun(ctx, integration, client, payload, now)
	if outcome != nil {
		return *outcome
	}

	// Params/tags are logged once, at run creation (MLflow rejects
	// re-logging a param key with a different value). Guard on SyncStatus
	// so a retried "created" event (e.g. run creation itself succeeded but
	// LogBatch failed) safely re-attempts only the missing step — MLflow's
	// LogBatch is idempotent for identical key/value pairs, so re-sending
	// the same snapshot on retry is safe even if some of it already landed.
	if link.SyncStatus == string(SyncStatusSynced) {
		return outbox.Outcome{Delivered: true}
	}

	encoded := EncodeParams(payload.Params)
	tags := runTags(payload, integration.ID)
	for k, v := range encoded.OverflowTags {
		tags[k] = v
	}
	if err := client.LogBatch(ctx, LogBatchRequest{RunID: link.MLflowRunID, Params: encoded.Params, Tags: tagMapToSlice(tags)}); err != nil {
		return e.retryOrDeadWithLink(ctx, integration, link, err, "log_batch_failed")
	}

	link.SyncStatus = string(SyncStatusSynced)
	link.LastSequence = ev.Sequence
	link.LastErrorCode = ""
	link.LastErrorMessage = ""
	syncedAt := now
	link.LastSyncedAt = &syncedAt
	if err := e.Repo.UpsertRunLink(ctx, link); err != nil {
		return retryOutcome(err, "run_link_upsert_failed")
	}
	return outbox.Outcome{Delivered: true}
}

// handlePipelineRunFinished implements design doc section 7.4. The outbox's
// per-aggregate ordering gate (design doc section 10.3) only blocks a later
// event while an earlier one for the same aggregate is still
// pending/delivering — it does not block on a "created" event that went
// StatusDead (retries exhausted, or a non-retryable error). If that
// happened, GetRunLink below finds no MLflowRunID and this intentionally
// does *not* create the run late: PipelineRunFinishedPayload carries only
// status/end-time, not the pipeline name/params/experiment "created"
// needs, and creating a run with nothing but a terminal status and no
// start-time metadata would be actively misleading in the MLflow UI. It
// reports Delivered instead — a run that never got an MLflow counterpart is
// a lost export, not a delivery failure, consistent with design doc section
// 4.3's "MLflow 장애나 불일치가 Piper run의 성공, 실패, 취소 상태를 바꾸지
// 않는다" (recovering it is the out-of-scope reconciler's job — design doc
// section 10.4).
func (e *Exporter) handlePipelineRunFinished(ctx context.Context, integration *MLflowIntegration, client Client, ev *outbox.Event, now time.Time) outbox.Outcome {
	var payload PipelineRunFinishedPayload
	if err := json.Unmarshal(ev.PayloadJSON, &payload); err != nil {
		return outbox.Outcome{Retryable: false, ErrorCode: "invalid_payload", ErrorMessage: "malformed pipeline_run.finished payload"}
	}

	link, err := e.Repo.GetRunLink(ctx, integration.ID, integration.ProjectID, string(SourceTypePipeline), payload.RunID)
	if err != nil {
		return retryOutcome(err, "run_link_lookup_failed")
	}
	if link == nil || link.MLflowRunID == "" {
		// "created" never even got as far as creating the remote run (it's
		// still pending/dead, or was never enqueued — e.g. the integration
		// was added after the run started). Nothing to finalize; the run
		// simply never had an MLflow-side counterpart, which is a valid,
		// non-retryable outcome — creating one now, out of order, with only
		// a terminal status and none of the start-time metadata, would be
		// actively misleading in the MLflow UI. Not a delivery failure.
		return outbox.Outcome{Delivered: true}
	}

	mlflowStatus, ok := PiperRunStatusToMLflow(payload.Status)
	if !ok {
		return outbox.Outcome{Retryable: false, ErrorCode: "unknown_run_status", ErrorMessage: "no MLflow status mapping for piper status " + payload.Status}
	}

	if link.SyncStatus == string(SyncStatusSynced) && link.LastSequence >= ev.Sequence {
		// Already applied (idempotent replay after a lease reclaim whose
		// original attempt actually succeeded remotely before crashing).
		return outbox.Outcome{Delivered: true}
	}

	endTimeMillis := payload.EndTime.UnixMilli()
	if err := client.UpdateRun(ctx, UpdateRunRequest{RunID: link.MLflowRunID, Status: mlflowStatus, EndTime: endTimeMillis}); err != nil {
		return e.retryOrDeadWithLink(ctx, integration, link, err, "update_run_failed")
	}

	link.SyncStatus = string(SyncStatusSynced)
	link.LastSequence = ev.Sequence
	link.LastErrorCode = ""
	link.LastErrorMessage = ""
	syncedAt := now
	link.LastSyncedAt = &syncedAt
	if err := e.Repo.UpsertRunLink(ctx, link); err != nil {
		return retryOutcome(err, "run_link_upsert_failed")
	}
	return outbox.Outcome{Delivered: true}
}

// ensureRun resolves (or creates) the experiment link and run link for
// payload, following design doc section 7.1's "search-before-create"
// dedupe rule (section 10.1: "experiment/run create: piper.* tag search로
// 중복 수렴") on every call, not only after a timeout — this is simpler than
// distinguishing "ambiguous" from "confirmed" failures and gives the same
// at-least-once-safe result.
func (e *Exporter) ensureRun(ctx context.Context, integration *MLflowIntegration, client Client, payload PipelineRunCreatedPayload, now time.Time) (*MLflowRunLink, *outbox.Outcome) {
	link, err := e.Repo.GetRunLink(ctx, integration.ID, integration.ProjectID, string(SourceTypePipeline), payload.RunID)
	if err != nil {
		o := retryOutcome(err, "run_link_lookup_failed")
		return nil, &o
	}
	if link != nil && link.MLflowRunID != "" {
		return link, nil
	}

	expLink, err := e.resolveExperiment(ctx, integration, client, payload)
	if err != nil {
		o := e.retryOrDead(err, "experiment_resolve_failed")
		return nil, &o
	}

	// Search before create (dedupe across retried/duplicate attempts —
	// design doc section 7.1/10.1).
	found, err := client.SearchRuns(ctx, SearchRunsRequest{
		ExperimentIDs: []string{expLink.MLflowExperimentID},
		Filter:        fmt.Sprintf("tags.piper.run_id = '%s' and tags.piper.integration_id = '%s'", payload.RunID, integration.ID),
		MaxResults:    1,
	})
	if err != nil {
		o := e.retryOrDead(err, "run_search_failed")
		return nil, &o
	}

	var mlflowRun *Run
	if len(found.Runs) > 0 {
		mlflowRun = found.Runs[0]
	} else {
		shortID := payload.RunID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		mlflowRun, err = client.CreateRun(ctx, CreateRunRequest{
			ExperimentID: expLink.MLflowExperimentID,
			RunName:      payload.PipelineName + "-" + shortID,
			StartTime:    payload.StartTime.UnixMilli(),
			Tags:         map[string]string{"piper.run_id": payload.RunID, "piper.integration_id": integration.ID},
		})
		if err != nil {
			o := e.retryOrDead(err, "run_create_failed")
			return nil, &o
		}
	}

	link = &MLflowRunLink{
		IntegrationID:      integration.ID,
		ProjectID:          integration.ProjectID,
		SourceType:         string(SourceTypePipeline),
		SourceID:           payload.RunID,
		MLflowExperimentID: expLink.MLflowExperimentID,
		MLflowRunID:        mlflowRun.RunID,
		MLflowRunURL:       runURL(integration.TrackingURI, expLink.MLflowExperimentID, mlflowRun.RunID),
		SyncStatus:         string(SyncStatusSyncing),
	}
	if err := e.Repo.UpsertRunLink(ctx, link); err != nil {
		o := retryOutcome(err, "run_link_upsert_failed")
		return nil, &o
	}
	return link, nil
}

// resolveExperiment implements design doc section 6.1's resolve-then-create
// (with get-by-name fallback) flow for the experiment mapping.
func (e *Exporter) resolveExperiment(ctx context.Context, integration *MLflowIntegration, client Client, payload PipelineRunCreatedPayload) (*MLflowExperimentLink, error) {
	groupKey := experimentGroupKey(payload.Experiment, payload.PipelineName)
	link, err := e.Repo.GetExperimentLink(ctx, integration.ID, integration.ProjectID, groupKey)
	if err != nil {
		return nil, err
	}
	if link != nil {
		return link, nil
	}

	experimentOrPipeline := payload.Experiment
	if experimentOrPipeline == "" {
		experimentOrPipeline = payload.PipelineName
	}
	name := experimentNameFromTemplate(integration.ExperimentTemplate, integration.ProjectID, experimentOrPipeline)

	exp, err := client.GetExperimentByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if exp == nil {
		exp, err = client.CreateExperiment(ctx, CreateExperimentRequest{Name: name})
		if err != nil {
			// A concurrent creator may have won the race (design doc
			// section 6.1: "동시에 두 worker가 create하더라도 name
			// conflict 응답 뒤 get-by-name으로 수렴해야 한다").
			if again, getErr := client.GetExperimentByName(ctx, name); getErr == nil && again != nil {
				exp = again
			} else {
				return nil, err
			}
		}
	}

	link = &MLflowExperimentLink{
		IntegrationID:      integration.ID,
		ProjectID:          integration.ProjectID,
		PiperGroupKey:      groupKey,
		MLflowExperimentID: exp.ExperimentID,
		MLflowName:         name,
	}
	if err := e.Repo.UpsertExperimentLink(ctx, link); err != nil {
		return nil, err
	}
	return link, nil
}

// retryOrDeadWithLink is retryOrDead plus persisting the redacted
// error onto the run link (design doc section 6.2's LastErrorCode/
// LastErrorMessage, surfaced by the REST API without ever including a raw
// remote response body).
func (e *Exporter) retryOrDeadWithLink(ctx context.Context, integration *MLflowIntegration, link *MLflowRunLink, err error, fallbackCode string) outbox.Outcome {
	outcome := e.retryOrDead(err, fallbackCode)
	link.LastErrorCode = outcome.ErrorCode
	link.LastErrorMessage = outcome.ErrorMessage
	if !outcome.Retryable {
		link.SyncStatus = string(SyncStatusDegraded)
	}
	if upsertErr := e.Repo.UpsertRunLink(ctx, link); upsertErr != nil {
		slog.Warn("mlflow exporter: failed persisting run link error state", "integration_id", integration.ID, "run_id", link.SourceID, "err", upsertErr)
	}
	return outcome
}

// retryOrDead classifies err (a *ClientError from the real HTTP Client, or
// anything else) into an outbox.Outcome per design doc section 10.2.
func (e *Exporter) retryOrDead(err error, fallbackCode string) outbox.Outcome {
	return retryOutcomeWithCode(err, fallbackCode)
}

var _ outbox.DeadNotifier = (*Exporter)(nil)

// NotifyDead implements outbox.DeadNotifier: it fires when the Dispatcher
// gives up on an event after Config.MaxAttemptsBeforeDead retries while
// Handle kept returning Retryable:true (e.g. a sustained NETWORK_ERROR) —
// the one dead-lettering path retryOrDeadWithLink can't already cover,
// since Handle has no visibility into the attempt cap and so never had a
// chance to mark the link degraded itself. Without this, a run link could
// sit at SyncStatus "synced" (last written by an earlier successful event
// for the same run) forever alongside a LastErrorCode from an event that
// outbox itself considers permanently failed — synced and dead at once.
// mlflow's enqueue.go sets AggregateID to the pipeline run ID for every
// event type, so it doubles as the run link's SourceID with no payload
// decode needed here.
func (e *Exporter) NotifyDead(ctx context.Context, ev *outbox.Event, outcome outbox.Outcome) {
	link, err := e.Repo.GetRunLink(ctx, ev.IntegrationID, ev.ProjectID, string(SourceTypePipeline), ev.AggregateID)
	if err != nil {
		slog.Warn("mlflow exporter: look up run link after event marked dead failed", "run_id", ev.AggregateID, "err", err)
		return
	}
	if link == nil || link.SyncStatus == string(SyncStatusDegraded) {
		return
	}
	link.SyncStatus = string(SyncStatusDegraded)
	link.LastErrorCode = outcome.ErrorCode
	link.LastErrorMessage = outcome.ErrorMessage
	if err := e.Repo.UpsertRunLink(ctx, link); err != nil {
		slog.Warn("mlflow exporter: sync run link after event marked dead failed", "run_id", ev.AggregateID, "err", err)
	}
}

func retryOutcome(err error, fallbackCode string) outbox.Outcome {
	return outbox.Outcome{Retryable: true, ErrorCode: fallbackCode, ErrorMessage: redactErr(err)}
}

func retryOutcomeWithCode(err error, fallbackCode string) outbox.Outcome {
	var ce *ClientError
	if e, ok := err.(*ClientError); ok {
		ce = e
	}
	code := fallbackCode
	msg := redactErr(err)
	retryable := true
	var retryAfter time.Duration
	if ce != nil {
		code = ErrorCode(err)
		msg = ce.Message
		retryable = ce.Retryable()
		retryAfter = ce.RetryAfter
	}
	return outbox.Outcome{Retryable: retryable, RetryAfter: retryAfter, ErrorCode: code, ErrorMessage: msg}
}

// redactErr renders err's message, truncated — this is a defense-in-depth
// backstop for the (fallback-code) cases above where err isn't already a
// redaction-aware *ClientError; every real remote call error already goes
// through http_client.go's newClientError/classifyNetworkError, which never
// carry raw response bodies or credentials in the first place.
func redactErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}

func tagMapToSlice(tags map[string]string) []Tag {
	out := make([]Tag, 0, len(tags))
	for k, v := range tags {
		out = append(out, Tag{Key: k, Value: v})
	}
	return out
}

// runURL builds a best-effort browser-facing MLflow run URL from the
// integration's TrackingURI (design doc section 6.2: "MLflow UI link").
// MLflow's UI route shape (#/experiments/{id}/runs/{id}) is a UI
// convention, not a documented REST API contract, so this is a
// best-effort convenience link, not a guaranteed-stable URL.
func runURL(trackingURI, experimentID, runID string) string {
	base := trackingURI
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	return fmt.Sprintf("%s/#/experiments/%s/runs/%s", base, experimentID, runID)
}
