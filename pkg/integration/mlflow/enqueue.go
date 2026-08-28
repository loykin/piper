package mlflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/loykin/piper/pkg/integration/outbox"
)

// resolveExportIntegration returns the project's enabled, ExportPipelines
// integration to export to, or (nil, nil) if there isn't one. v1 supports
// at most one Default=true integration per project (design doc section
// 5.1), so the default integration is what "the" pipeline export target
// means for a project.
func resolveExportIntegration(ctx context.Context, repo Repository, projectID string) (*MLflowIntegration, error) {
	integration, err := repo.GetDefaultIntegration(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if integration == nil || integration.IsDeleted() || !integration.Enabled || !integration.ExportPipelines {
		return nil, nil
	}
	return integration, nil
}

// EnqueuePipelineRunCreated durably records a pipeline_run.created outbox
// event (design doc section 7.1) for runID, if and only if projectID has an
// enabled default integration with ExportPipelines=true. This is called
// from both of internal/runlifecycle's StartRun call sites (the immediate-
// dispatch path and the future-scheduled-run path in
// StartRunFromAPIWithID) — mirroring where run.Run.StorageBackend is
// stamped, per this task's brief — since design doc section 7.1's payload
// explicitly carries a "start/scheduled time" field, meaning a
// future-scheduled run's creation is exported too, not only an immediately
// dispatched one.
//
// This performs no outbound MLflow call — only a local read (the
// integration lookup) and a local durable write (the outbox insert) — so it
// is safe to call inline on the synchronous run-creation path (design doc
// section 4.3: "MLflow API 호출은 Pipeline/Notebook의 synchronous lifecycle
// path에 두지 않는다" — this enqueues work for the async dispatcher, it does
// not talk to MLflow itself). Errors are returned for the caller to log,
// never to fail run creation on — see runlifecycle.Deps.EnqueuePipelineCreated's
// doc comment.
//
// runURL is a caller-supplied best-effort link (design doc section 7.1's
// "Piper run URL을 만들 수 있는 public base URL" — Piper has no configured
// public base URL today, see piper.go's wiring, so callers pass a relative
// API path here).
func EnqueuePipelineRunCreated(ctx context.Context, repo Repository, outboxRepo outbox.Repository, projectID, runID string, params map[string]any, pipelineName string, pipelineVersion int, experiment, createdBy, runtimeType, runURL string, startTime time.Time) error {
	integration, err := resolveExportIntegration(ctx, repo, projectID)
	if err != nil {
		return fmt.Errorf("mlflow: resolve export integration: %w", err)
	}
	if integration == nil {
		return nil
	}
	payload := PipelineRunCreatedPayload{
		ProjectID:       projectID,
		RunID:           runID,
		PipelineName:    pipelineName,
		PipelineVersion: pipelineVersion,
		Experiment:      experiment,
		Params:          params,
		CreatedBy:       createdBy,
		RuntimeType:     runtimeType,
		StartTime:       startTime,
		RunURL:          runURL,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mlflow: encode pipeline_run.created payload: %w", err)
	}
	return outboxRepo.Enqueue(ctx, &outbox.Event{
		IntegrationID: integration.ID,
		ProjectID:     projectID,
		AggregateType: outbox.AggregateTypePipelineRun,
		AggregateID:   runID,
		EventType:     EventTypePipelineRunCreated,
		PayloadJSON:   body,
	})
}

// EnqueuePipelineRunFinished durably records a pipeline_run.finished
// outbox event (design doc section 7.4) for runID, under the same
// enabled-default-integration gate as EnqueuePipelineRunCreated. Intended
// to be wired into the queue's terminal-transition hook (queue.OnRunOutcome
// — already fired asynchronously, after the DB CAS committing the terminal
// status has applied, via queue.go's appendEffect) rather than into
// internal/queue itself, keeping this package's dependency out of the
// queue's hot path.
func EnqueuePipelineRunFinished(ctx context.Context, repo Repository, outboxRepo outbox.Repository, projectID, runID, status string, endTime time.Time) error {
	integration, err := resolveExportIntegration(ctx, repo, projectID)
	if err != nil {
		return fmt.Errorf("mlflow: resolve export integration: %w", err)
	}
	if integration == nil {
		return nil
	}
	payload := PipelineRunFinishedPayload{ProjectID: projectID, RunID: runID, Status: status, EndTime: endTime}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mlflow: encode pipeline_run.finished payload: %w", err)
	}
	return outboxRepo.Enqueue(ctx, &outbox.Event{
		IntegrationID: integration.ID,
		ProjectID:     projectID,
		AggregateType: outbox.AggregateTypePipelineRun,
		AggregateID:   runID,
		EventType:     EventTypePipelineRunFinished,
		PayloadJSON:   body,
	})
}
