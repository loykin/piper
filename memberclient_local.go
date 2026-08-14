package piper

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

// localMemberClient implements memberclient.Client for the single-install
// case: Home calls its one Local Member in-process (fed.md §13.11). It is a
// thin translation layer over *Piper's existing methods/repos — the
// execution logic itself is not duplicated or relocated, only reached
// through the new Client boundary instead of directly.
type localMemberClient struct {
	p *Piper
}

// NewLocalMemberClient wraps p to satisfy memberclient.Client in-process.
func NewLocalMemberClient(p *Piper) memberclient.Client {
	return &localMemberClient{p: p}
}

// withProjectContext pins ctx's project.Context to exactly what Home
// resolved (ref/auth), rather than trusting whatever project.Context the
// incoming ctx already happens to carry — Member acts on what it was
// explicitly handed, matching fed.md §10.10's "Member validates the
// authorization context" invariant even though, for the in-process Local
// Member, Home and Member share the same request lifecycle.
func withProjectContext(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef) context.Context {
	return project.WithContext(ctx, project.Context{ID: ref.ProjectID, Role: auth.Role})
}

func toRunSummary(r *run.Run) memberclient.RunSummary {
	version := r.VersionFromYAML()
	redacted := r.Redact()
	return memberclient.RunSummary{
		ID:              redacted.ID,
		ProjectID:       redacted.ProjectID,
		ScheduleID:      redacted.ScheduleID,
		Experiment:      redacted.Experiment,
		PipelineName:    redacted.PipelineName,
		PipelineVersion: version,
		Status:          redacted.Status,
		StartedAt:       redacted.StartedAt,
		EndedAt:         redacted.EndedAt,
		ScheduledAt:     redacted.ScheduledAt,
		PipelineYAML:    redacted.PipelineYAML,
		ParamsJSON:      redacted.ParamsJSON,
		CreatedBy:       redacted.CreatedBy,
	}
}

func toStepSummary(s *run.Step) memberclient.StepSummary {
	return memberclient.StepSummary{
		ProjectID: s.ProjectID,
		RunID:     s.RunID,
		StepName:  s.StepName,
		Status:    s.Status,
		StartedAt: s.StartedAt,
		EndedAt:   s.EndedAt,
		Error:     s.Error,
		Attempts:  s.Attempts,
	}
}

func toStepSummaries(steps []*run.Step) []memberclient.StepSummary {
	out := make([]memberclient.StepSummary, 0, len(steps))
	for _, s := range steps {
		out = append(out, toStepSummary(s))
	}
	return out
}

func (l *localMemberClient) SubmitRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
	ctx = withProjectContext(ctx, auth, ref)
	if req.IdempotencyKey == "" || l.p.repos.Submission == nil {
		runID, err := l.p.startRunFromAPI(ctx, req.YAML, req.Params, req.Vars, req.Experiment)
		if err != nil {
			return memberclient.SubmitRunResponse{}, err
		}
		return memberclient.SubmitRunResponse{RunID: runID}, nil
	}

	l.p.submissionMu.Lock()
	defer l.p.submissionMu.Unlock()
	payload, err := json.Marshal(struct {
		YAML       string
		Params     map[string]any
		Experiment string
		Vars       BuiltinVars
	}{req.YAML, req.Params, req.Experiment, req.Vars})
	if err != nil {
		return memberclient.SubmitRunResponse{}, fmt.Errorf("encode idempotent submission: %w", err)
	}
	sum := sha256.Sum256(payload)
	requestHash := base64.RawURLEncoding.EncodeToString(sum[:])
	submission, _, err := l.p.repos.Submission.Claim(ctx, &run.Submission{
		ProjectID: ref.ProjectID, Key: req.IdempotencyKey, RequestHash: requestHash,
		RunID: genRunID(), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return memberclient.SubmitRunResponse{}, err
	}
	if submission.RequestHash != requestHash {
		return memberclient.SubmitRunResponse{}, fmt.Errorf("idempotency key was already used for a different Run request")
	}
	if existing, err := l.p.repos.Run.Get(ctx, ref.ProjectID, submission.RunID); err != nil {
		return memberclient.SubmitRunResponse{}, err
	} else if existing != nil {
		return memberclient.SubmitRunResponse{RunID: submission.RunID}, nil
	}
	runID, err := l.p.startRunFromAPIWithID(ctx, submission.RunID, req.YAML, req.Params, req.Vars, req.Experiment)
	if err != nil {
		if existing, getErr := l.p.repos.Run.Get(ctx, ref.ProjectID, submission.RunID); getErr == nil && existing == nil {
			_ = l.p.repos.Submission.Delete(ctx, ref.ProjectID, req.IdempotencyKey)
		}
		return memberclient.SubmitRunResponse{}, err
	}
	return memberclient.SubmitRunResponse{RunID: runID}, nil
}

func (l *localMemberClient) SubmitSweep(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitSweepRequest) (memberclient.SubmitSweepResponse, error) {
	ctx = withProjectContext(ctx, auth, ref)
	trials := make([]run.SweepTrial, 0, len(req.Runs))
	for _, t := range req.Runs {
		trials = append(trials, run.SweepTrial{Params: t.Params})
	}
	resp, err := l.p.startSweep(ctx, ref.ProjectID, run.SweepRequest{YAML: req.YAML, Experiment: req.Experiment, Runs: trials})
	if err != nil {
		return memberclient.SubmitSweepResponse{}, err
	}
	return memberclient.SubmitSweepResponse{Experiment: resp.Experiment, RunIDs: resp.RunIDs}, nil
}

func (l *localMemberClient) ListRuns(ctx context.Context, _ memberclient.AuthContext, ref project.ProjectRef, req memberclient.ListRunsRequest) (memberclient.ListRunsResponse, error) {
	filter := run.RunFilter{
		Experiment:   req.Experiment,
		PipelineName: req.PipelineName,
		ScheduleID:   req.ScheduleID,
		Status:       req.Status,
		MetricStep:   req.MetricStep,
		MetricKey:    req.MetricKey,
		MetricOrder:  req.MetricOrder,
		Limit:        req.Limit,
		Offset:       req.Offset,
	}
	runs, err := l.p.repos.Run.List(ctx, ref.ProjectID, filter)
	if err != nil {
		return memberclient.ListRunsResponse{}, err
	}
	resp := memberclient.ListRunsResponse{Runs: make([]memberclient.RunSummary, 0, len(runs))}
	for _, r := range runs {
		resp.Runs = append(resp.Runs, toRunSummary(r))
	}
	if req.Limit > 0 {
		total, err := l.p.repos.Run.Count(ctx, ref.ProjectID, filter)
		if err != nil {
			return memberclient.ListRunsResponse{}, err
		}
		resp.Total = total
	}
	if req.IncludeSteps {
		runIDs := make([]string, 0, len(runs))
		for _, r := range runs {
			runIDs = append(runIDs, r.ID)
		}
		stepsByRun, err := l.p.repos.Step.ListByRuns(ctx, ref.ProjectID, runIDs)
		if err != nil {
			return memberclient.ListRunsResponse{}, err
		}
		resp.Steps = make(map[string][]memberclient.StepSummary, len(stepsByRun))
		for runID, steps := range stepsByRun {
			resp.Steps[runID] = toStepSummaries(steps)
		}
	}
	return resp, nil
}

func (l *localMemberClient) GetRun(ctx context.Context, _ memberclient.AuthContext, ref project.ProjectRef, runID string) (memberclient.RunDetail, error) {
	r, err := l.p.repos.Run.Get(ctx, ref.ProjectID, runID)
	if err != nil || r == nil {
		return memberclient.RunDetail{}, memberclient.ErrRunNotFound
	}
	steps, err := l.p.repos.Step.List(ctx, ref.ProjectID, runID)
	if err != nil {
		steps = nil // best-effort, matches the handler's prior slog.Warn-and-continue behavior
	}
	return memberclient.RunDetail{Run: toRunSummary(r), Steps: toStepSummaries(steps)}, nil
}

func (l *localMemberClient) CancelRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) error {
	ctx = withProjectContext(ctx, auth, ref)
	return l.p.CancelRun(ctx, runID)
}

func (l *localMemberClient) RerunRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string, failedOnly bool) (string, error) {
	ctx = withProjectContext(ctx, auth, ref)
	return l.p.RerunRun(ctx, runID, failedOnly)
}

func (l *localMemberClient) DeleteRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) error {
	ctx = withProjectContext(ctx, auth, ref)
	return l.p.DeleteRun(ctx, runID)
}

func (l *localMemberClient) ListSteps(ctx context.Context, _ memberclient.AuthContext, ref project.ProjectRef, runID string) ([]memberclient.StepSummary, error) {
	steps, err := l.p.repos.Step.List(ctx, ref.ProjectID, runID)
	if err != nil {
		return nil, err
	}
	return toStepSummaries(steps), nil
}

func (l *localMemberClient) RetryStep(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID, stepName string) (string, error) {
	ctx = withProjectContext(ctx, auth, ref)
	return l.p.RetryStep(ctx, runID, stepName)
}

func (l *localMemberClient) QueryLogs(_ context.Context, _ memberclient.AuthContext, ref project.ProjectRef, runID, stepName string, afterID int64) ([]*logstore.Line, error) {
	return l.p.logs.Query(ref.ProjectID, runID, stepName, afterID)
}

func (l *localMemberClient) QueryMetrics(_ context.Context, _ memberclient.AuthContext, ref project.ProjectRef, runID, stepName string) ([]*logstore.Metric, error) {
	if l.p.metrics == nil {
		return nil, nil
	}
	return l.p.metrics.QueryMetrics(ref.ProjectID, runID, stepName)
}

func (l *localMemberClient) ListArtifacts(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) ([]any, error) {
	ctx = withProjectContext(ctx, auth, ref)
	return (&piperArtifacts{p: l.p}).List(ctx, runID)
}

func (l *localMemberClient) ServeArtifact(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, w http.ResponseWriter, r *http.Request, runID, step, path string) {
	ctx = withProjectContext(ctx, auth, ref)
	r = r.WithContext(ctx)
	(&piperArtifacts{p: l.p}).ServeDownload(w, r, runID, step, path)
}
