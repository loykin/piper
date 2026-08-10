package scheduler

import (
	"fmt"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/pkg/pipeline/run"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

// runFinalizeRequest mirrors the unexported type of the same name in
// pipeline_db_handlers.go — duplicated here (not imported: that's the root
// `piper` package, which worker code must not depend on) since it's just a
// JSON wire shape, not shared Go state.
type runFinalizeRequest struct {
	ProjectID string     `json:"project_id"`
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// OutboxReporter is a StepReporter backed by a driver.RequestOutbox, so a
// step_upsert/run_finalize call made while the tunnel is down is durably
// retried instead of lost (see driver.RequestOutbox's doc comment for why
// this wrapper is necessary on top of grpcagent.Client.SendRequest).
type OutboxReporter struct {
	outbox    *pdriver.RequestOutbox
	projectID string
	runID     string
}

func NewOutboxReporter(outbox *pdriver.RequestOutbox, projectID, runID string) *OutboxReporter {
	return &OutboxReporter{outbox: outbox, projectID: projectID, runID: runID}
}

func (r *OutboxReporter) UpsertStep(s *run.Step) error {
	// id includes attempt+status so a later, different transition for the
	// same step doesn't overwrite (and thus lose) an not-yet-delivered
	// earlier one still pending in the outbox — each is a distinct entry.
	id := fmt.Sprintf("%s:%s:step_upsert:%d:%s", r.runID, s.StepName, s.Attempts, s.Status)
	return r.outbox.Enqueue(id, iagent.MethodPipelineStepUpsert, s)
}

func (r *OutboxReporter) FinalizeRun(status string, endedAt time.Time) error {
	id := fmt.Sprintf("%s:run_finalize", r.runID)
	req := runFinalizeRequest{
		ProjectID: r.projectID,
		ID:        r.runID,
		Status:    status,
		EndedAt:   &endedAt,
	}
	return r.outbox.Enqueue(id, iagent.MethodPipelineRunFinalize, req)
}
