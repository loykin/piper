package runlifecycle

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/internal/queue"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
	"gopkg.in/yaml.v3"
)

// HandleRunSuccess is called (in a goroutine, via Queue.OnRunSuccess) when a
// queued run completes successfully. It triggers on_success.deploy if
// configured in the pipeline spec.
func (m *Manager) HandleRunSuccess(ctx context.Context, runID string, pl *pipeline.Pipeline) {
	if pl.Spec.OnSuccess == nil || pl.Spec.OnSuccess.Deploy == nil {
		return
	}
	trigger := pl.Spec.OnSuccess.Deploy
	projectContext, _ := project.FromContext(ctx)
	svc, err := m.deps.ServingRepo.Get(ctx, projectContext.ID, trigger.Service)
	if err != nil || svc == nil {
		return
	}
	if svc.YAML == "" {
		return
	}
	// Re-deploy with the new run's artifact
	ms, err := serving.Parse([]byte(svc.YAML))
	if err != nil {
		return
	}
	if ms.Spec.Model.FromArtifact != nil {
		ms.Spec.Model.FromArtifact.Run = runID
	}
	updatedYAML, _ := yaml.Marshal(ms)
	if _, err := m.deps.DeployService(ctx, projectContext.ID, updatedYAML); err != nil {
		slog.Warn("auto-deploy on run success failed", "run_id", runID, "service", trigger.Service, "err", err)
	}
}

// ListRunsAcrossProjects lists runs matching filter across every project.
func (m *Manager) ListRunsAcrossProjects(ctx context.Context, filter run.RunFilter) ([]*run.Run, error) {
	projects, err := m.deps.ProjectRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	var runs []*run.Run
	for _, projectRecord := range projects {
		projectRuns, err := m.deps.RunRepo.List(ctx, projectRecord.ID, filter)
		if err != nil {
			return nil, err
		}
		runs = append(runs, projectRuns...)
	}
	return runs, nil
}

// RecoverInterruptedRuns re-attaches DB-"running" runs the in-memory queue
// lost track of (server restart, or a periodic reconcile pass — see
// piper.go's runCleanup): rebuilds the DAG, replays step state, resolves
// credential env, and re-adds the run to the queue.
func (m *Manager) RecoverInterruptedRuns(ctx context.Context) {
	runs, err := m.ListRunsAcrossProjects(ctx, run.RunFilter{Status: run.StatusRunning})
	if err != nil {
		slog.Warn("recover running runs failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, r := range runs {
		if m.deps.Queue.IsTracking(r.ID) {
			// Still actively being processed by this queue instance — leave
			// it alone. Without this guard, calling this function again
			// after startup (see runCleanup's periodic reconciler pass)
			// would re-add a live run and corrupt its in-memory state.
			continue
		}
		if r.PipelineYAML == "" {
			// No YAML — can't reconstruct DAG, mark failed.
			if err := m.deps.RunRepo.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now); err != nil {
				slog.Warn("recover run failed", "run_id", r.ID, "err", err)
			}
			continue
		}
		pl, err := pipeline.Parse([]byte(r.PipelineYAML))
		if err != nil {
			slog.Warn("recover: parse pipeline failed", "run_id", r.ID, "err", err)
			_ = m.deps.RunRepo.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		dag, err := pipeline.BuildDAG(pl)
		if err != nil {
			slog.Warn("recover: build dag failed", "run_id", r.ID, "err", err)
			_ = m.deps.RunRepo.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		steps, _ := m.deps.StepRepo.List(ctx, r.ProjectID, r.ID)
		var recovered []queue.RecoveredStep
		for _, s := range steps {
			switch s.Status {
			case "done", "skipped":
				recovered = append(recovered, queue.RecoveredStep{Name: s.StepName, Done: true})
			case "running":
				startedAt := now
				if s.StartedAt != nil {
					startedAt = *s.StartedAt
				}
				recovered = append(recovered, queue.RecoveredStep{Name: s.StepName, StartedAt: startedAt, Attempts: s.Attempts})
			}
		}
		var params map[string]any
		if r.ParamsJSON != "" {
			_ = json.Unmarshal([]byte(r.ParamsJSON), &params)
		}
		outputDir := runWorkspaceDir(m.deps.OutputDir, r.ID)
		envByStep, err := m.deps.Credentials.ResolvePipelineEnv(ctx, r.ProjectID, r.ID, pl)
		if err != nil {
			slog.Warn("recover: resolve credential env failed", "run_id", r.ID, "err", err)
			_ = m.deps.RunRepo.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		m.deps.Queue.RecoverWithEnv(ctx, r.ProjectID, pl, dag, r.ID, ".", outputDir, proto.BuiltinVars{ScheduledAt: r.ScheduledAt}, params, recovered, envByStep)
	}
}
