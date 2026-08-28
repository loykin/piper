package runlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

func genRunID() string { return uuid.NewString() }

// GenScheduleID generates a new schedule ID. Exported for member_project.go's
// schedule.HandlerDeps/template.HandlerDeps wiring.
func GenScheduleID() string { return uuid.NewString() }

func encodeParams(params map[string]any) string {
	if params == nil {
		return "{}"
	}
	b, err := json.Marshal(params)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// runWorkspaceDir returns the Workspace root for a run (docs/backend/develop.md
// "Workspace vs. Artifact Repository"): OutputDir/<runID>. Per-step execution
// and logs live under <step> subdirectories of this path — see runner.go's
// stepOutputDir, the directory a Command step actually executes in and the
// one uploadOutputs later reads "outputs:" files from.
func runWorkspaceDir(outputDirBase, runID string) string {
	return filepath.Join(outputDirBase, runID)
}

// StartRunOptions holds parameters for enqueuing a new distributed run.
type StartRunOptions struct {
	RunID      string
	ProjectID  string
	ScheduleID string
	Experiment string
	Params     map[string]any
	Vars       proto.BuiltinVars
	YAML       string // raw YAML, persisted to DB
}

// StartRun is the single entry point for enqueuing a pipeline run. Both the
// HTTP API and the scheduler go through here. It creates the DB record,
// initialises step rows, enqueues the DAG, and fires OnRunStart.
func (m *Manager) StartRun(ctx context.Context, pl *pipeline.Pipeline, dag *pipeline.DAG, opts StartRunOptions) (string, error) {
	if err := pipeline.ValidateRuntime(pl, m.deps.RuntimeType); err != nil {
		return "", err
	}
	for _, outcome := range []*pipeline.OnOutcome{pl.Spec.OnSuccess, pl.Spec.OnFailure} {
		if outcome == nil {
			continue
		}
		for _, action := range outcome.Notify {
			if err := m.deps.Credentials.ValidateNotificationCredential(ctx, opts.ProjectID, action.CredentialRef); err != nil {
				return "", fmt.Errorf("notification credential %q: %w", action.CredentialRef, err)
			}
		}
	}
	runID := opts.RunID
	if runID == "" {
		runID = genRunID()
	}
	outputDir := runWorkspaceDir(m.deps.OutputDir, runID)
	now := time.Now().UTC()
	if opts.Vars.RunStartedAt == nil {
		opts.Vars.RunStartedAt = &now
	}

	r := &run.Run{
		ID:             runID,
		ProjectID:      opts.ProjectID,
		ScheduleID:     opts.ScheduleID,
		Experiment:     opts.Experiment,
		PipelineName:   pl.Metadata.Name,
		Status:         run.StatusRunning,
		StartedAt:      now,
		ScheduledAt:    opts.Vars.ScheduledAt,
		PipelineYAML:   opts.YAML,
		ParamsJSON:     encodeParams(opts.Params),
		StorageBackend: m.deps.StorageIdentity,
	}
	if identity, ok := security.IdentityFromContext(ctx); ok {
		r.CreatedBy = identity.ID
	}
	if err := m.deps.RunRepo.Create(ctx, r); err != nil {
		return "", fmt.Errorf("create run: %w", err)
	}

	for _, s := range pl.Spec.Steps {
		if err := m.deps.StepRepo.Upsert(ctx, &run.Step{
			ProjectID: opts.ProjectID,
			RunID:     runID,
			StepName:  s.Name,
			Status:    "pending",
		}); err != nil {
			slog.Warn("init step failed", "run_id", runID, "step", s.Name, "err", err)
		}
	}

	envByStep, err := m.deps.Credentials.ResolvePipelineEnv(ctx, opts.ProjectID, runID, pl)
	if err != nil {
		now := time.Now().UTC()
		_ = m.deps.RunRepo.UpdateStatus(ctx, opts.ProjectID, runID, run.StatusFailed, &now)
		return "", err
	}

	m.deps.Queue.AddWithEnv(ctx, opts.ProjectID, pl, dag, runID, ".", outputDir, opts.Vars, opts.Params, envByStep)
	slog.Info("event", "type", "run.started", "run_id", runID, "pipeline", pl.Metadata.Name)

	if m.deps.OnRunStart != nil {
		go m.deps.OnRunStart(ctx, runID, pl)
	}

	return runID, nil
}

// StartSweep submits multiple runs from one YAML with different params.
// On partial failure it cancels already-submitted runs (best-effort).
func (m *Manager) StartSweep(ctx context.Context, projectID string, req run.SweepRequest) (run.SweepResponse, error) {
	pl, err := pipeline.Parse([]byte(req.YAML))
	if err != nil {
		return run.SweepResponse{}, fmt.Errorf("parse pipeline: %w", err)
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return run.SweepResponse{}, fmt.Errorf("build dag: %w", err)
	}

	runIDs := make([]string, 0, len(req.Runs))
	for i, trial := range req.Runs {
		runID, err := m.StartRun(ctx, pl, dag, StartRunOptions{
			ProjectID:  projectID,
			Experiment: req.Experiment,
			Params:     trial.Params,
			YAML:       req.YAML,
		})
		if err != nil {
			now := time.Now().UTC()
			for _, id := range runIDs {
				_ = m.deps.RunRepo.UpdateStatus(ctx, projectID, id, run.StatusCanceled, &now)
			}
			return run.SweepResponse{}, fmt.Errorf("trial %d: %w", i, err)
		}
		runIDs = append(runIDs, runID)
	}
	return run.SweepResponse{Experiment: req.Experiment, RunIDs: runIDs}, nil
}

// StartRunFromAPI handles creating a run from the HTTP API, including
// future-scheduled runs and immediate dispatch.
func (m *Manager) StartRunFromAPI(ctx context.Context, yaml string, params map[string]any, vars proto.BuiltinVars, experiment string) (string, error) {
	return m.StartRunFromAPIWithID(ctx, "", yaml, params, vars, experiment)
}

func (m *Manager) StartRunFromAPIWithID(ctx context.Context, runID, yaml string, params map[string]any, vars proto.BuiltinVars, experiment string) (string, error) {
	projectContext, _ := project.FromContext(ctx)

	pl, err := pipeline.Parse([]byte(yaml))
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return "", fmt.Errorf("build dag: %w", err)
	}

	// Future-scheduled runs are stored but not enqueued yet.
	now := time.Now().UTC()
	if vars.ScheduledAt != nil && vars.ScheduledAt.After(now) {
		if runID == "" {
			runID = genRunID()
		}
		newRun := &run.Run{
			ID:             runID,
			ProjectID:      projectContext.ID,
			Experiment:     experiment,
			PipelineName:   pl.Metadata.Name,
			Status:         run.StatusScheduled,
			StartedAt:      now,
			ScheduledAt:    vars.ScheduledAt,
			PipelineYAML:   yaml,
			ParamsJSON:     encodeParams(params),
			StorageBackend: m.deps.StorageIdentity,
		}
		if identity, ok := security.IdentityFromContext(ctx); ok {
			newRun.CreatedBy = identity.ID
		}
		if err := m.deps.RunRepo.Create(ctx, newRun); err != nil {
			return "", err
		}
		return runID, nil
	}

	return m.StartRun(ctx, pl, dag, StartRunOptions{
		RunID:      runID,
		ProjectID:  projectContext.ID,
		Experiment: experiment,
		Params:     params,
		Vars:       vars,
		YAML:       yaml,
	})
}

// SubmitRun is the idempotency-aware entry point used by the Member client
// boundary (fed.md §13.11): when idempotencyKey is set and a submission
// repository is configured, a replayed request with the same key+payload
// returns the original run instead of creating a duplicate.
func (m *Manager) SubmitRun(ctx context.Context, projectID, idempotencyKey, yamlText string, params map[string]any, vars proto.BuiltinVars, experiment string) (string, error) {
	if idempotencyKey == "" || m.deps.SubmissionRepo == nil {
		return m.StartRunFromAPI(ctx, yamlText, params, vars, experiment)
	}

	m.submissionMu.Lock()
	defer m.submissionMu.Unlock()
	payload, err := json.Marshal(struct {
		YAML       string
		Params     map[string]any
		Experiment string
		Vars       proto.BuiltinVars
	}{yamlText, params, experiment, vars})
	if err != nil {
		return "", fmt.Errorf("encode idempotent submission: %w", err)
	}
	sum := sha256.Sum256(payload)
	requestHash := base64.RawURLEncoding.EncodeToString(sum[:])
	submission, _, err := m.deps.SubmissionRepo.Claim(ctx, &run.Submission{
		ProjectID: projectID, Key: idempotencyKey, RequestHash: requestHash,
		RunID: genRunID(), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}
	if submission.RequestHash != requestHash {
		return "", fmt.Errorf("idempotency key was already used for a different Run request")
	}
	if existing, err := m.deps.RunRepo.Get(ctx, projectID, submission.RunID); err != nil {
		return "", err
	} else if existing != nil {
		return submission.RunID, nil
	}
	runID, err := m.StartRunFromAPIWithID(ctx, submission.RunID, yamlText, params, vars, experiment)
	if err != nil {
		if existing, getErr := m.deps.RunRepo.Get(ctx, projectID, submission.RunID); getErr == nil && existing == nil {
			_ = m.deps.SubmissionRepo.Delete(ctx, projectID, idempotencyKey)
		}
		return "", err
	}
	return runID, nil
}

// CancelRun cancels a queued or running run.
func (m *Manager) CancelRun(ctx context.Context, runID string) error {
	projectContext, _ := project.FromContext(ctx)
	return m.deps.Queue.Cancel(ctx, projectContext.ID, runID)
}

// RerunRun re-executes a run, optionally limiting to failed steps only.
func (m *Manager) RerunRun(ctx context.Context, runID string, failedOnly bool) (string, error) {
	projectContext, _ := project.FromContext(ctx)
	prev, err := m.deps.RunRepo.Get(ctx, projectContext.ID, runID)
	if err != nil || prev == nil {
		return "", fmt.Errorf("run %q not found", runID)
	}
	if prev.PipelineYAML == "" {
		return "", fmt.Errorf("run %q has no stored pipeline yaml", runID)
	}
	var params map[string]any
	if prev.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(prev.ParamsJSON), &params)
	}
	pl, err := pipeline.Parse([]byte(prev.PipelineYAML))
	if err != nil {
		return "", fmt.Errorf("parse previous run yaml: %w", err)
	}
	if failedOnly {
		steps, err := m.deps.StepRepo.List(ctx, projectContext.ID, runID)
		if err != nil {
			return "", err
		}
		failed := map[string]bool{}
		for _, s := range steps {
			if s.Status == "failed" {
				failed[s.StepName] = true
			}
		}
		if len(failed) == 0 {
			return "", fmt.Errorf("run %q has no failed steps", runID)
		}
		var filtered []pipeline.Step
		for _, s := range pl.Spec.Steps {
			if failed[s.Name] {
				s.DependsOn = nil
				filtered = append(filtered, s)
			}
		}
		pl.Spec.Steps = filtered
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return "", fmt.Errorf("build dag: %w", err)
	}
	return m.StartRun(ctx, pl, dag, StartRunOptions{
		ProjectID: prev.ProjectID,
		Params:    params,
		YAML:      prev.PipelineYAML,
	})
}

// RetryStep retries a single failed step within a run.
func (m *Manager) RetryStep(ctx context.Context, runID, stepName string) (string, error) {
	projectContext, _ := project.FromContext(ctx)
	prev, err := m.deps.RunRepo.Get(ctx, projectContext.ID, runID)
	if err != nil || prev == nil {
		return "", fmt.Errorf("run %q not found", runID)
	}
	steps, err := m.deps.StepRepo.List(ctx, projectContext.ID, runID)
	if err != nil {
		return "", err
	}
	foundFailed := false
	for _, s := range steps {
		if s.StepName == stepName && s.Status == "failed" {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		return "", fmt.Errorf("step %q is not failed in run %q", stepName, runID)
	}
	if prev.PipelineYAML == "" {
		return "", fmt.Errorf("run %q has no stored pipeline yaml", runID)
	}
	var params map[string]any
	if prev.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(prev.ParamsJSON), &params)
	}
	pl, err := pipeline.Parse([]byte(prev.PipelineYAML))
	if err != nil {
		return "", fmt.Errorf("parse previous run yaml: %w", err)
	}
	for _, s := range pl.Spec.Steps {
		if s.Name == stepName {
			s.DependsOn = nil
			pl.Spec.Steps = []pipeline.Step{s}
			dag, err := pipeline.BuildDAG(pl)
			if err != nil {
				return "", fmt.Errorf("build dag: %w", err)
			}
			return m.StartRun(ctx, pl, dag, StartRunOptions{ProjectID: prev.ProjectID, Params: params, YAML: prev.PipelineYAML})
		}
	}
	return "", fmt.Errorf("step %q not found in pipeline yaml", stepName)
}

// DeleteRunWithArtifacts deletes the run record and then its artifacts.
// The DB row goes first: it's the authoritative reference, and losing it
// after a successful artifact delete would just mean a 404 on a run that's
// genuinely gone. Doing it the other way — as this used to — risked the
// opposite: an artifact-delete failure left the DB row erased anyway, so the
// artifacts became permanently unreachable orphans (nothing left pointing at
// them). cleanupOrphanArtifacts (piper.go) sweeps up whatever this order
// still misses (e.g. a crash between the two steps).
func (m *Manager) DeleteRunWithArtifacts(ctx context.Context, runID string) error {
	projectContext, _ := project.FromContext(ctx)
	if err := m.deps.RunDeleter.DeleteRun(ctx, projectContext.ID, runID); err != nil {
		return err
	}
	if err := m.deps.DeleteArtifacts(ctx, m.deps.Store, runID); err != nil {
		slog.Warn("delete artifacts failed", "run_id", runID, "err", err)
	}
	if err := m.deps.DeleteWorkspace(m.deps.OutputDir, runID); err != nil {
		slog.Warn("delete run workspace failed", "run_id", runID, "err", err)
	}
	return nil
}

// DeleteRunsWithArtifacts is the batch variant of DeleteRunWithArtifacts.
func (m *Manager) DeleteRunsWithArtifacts(ctx context.Context, runIDs []string) error {
	projectContext, _ := project.FromContext(ctx)
	if err := m.deps.RunDeleter.DeleteRuns(ctx, projectContext.ID, runIDs); err != nil {
		return err
	}
	for _, runID := range runIDs {
		if err := m.deps.DeleteArtifacts(ctx, m.deps.Store, runID); err != nil {
			slog.Warn("delete artifacts failed", "run_id", runID, "err", err)
		}
		if err := m.deps.DeleteWorkspace(m.deps.OutputDir, runID); err != nil {
			slog.Warn("delete run workspace failed", "run_id", runID, "err", err)
		}
	}
	return nil
}
