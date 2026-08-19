package runlifecycle

import (
	"context"
	"log/slog"
	"time"

	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

// retentionScheduleBatch bounds how many overflow runs CleanupScheduleRetention
// deletes per schedule per cycle, so a schedule with a very long backlog (e.g.
// max_runs just lowered on a schedule with years of history) drains over
// several cycles instead of loading its entire run history in one pass.
const retentionScheduleBatch = 200

// CleanupRetention applies RunTTL/ArtifactTTL: deletes expired runs (with
// artifacts) or just artifacts. Called periodically from piper.go's
// runCleanup ticker.
func (m *Manager) CleanupRetention(ctx context.Context) {
	runTTL := m.deps.RunTTL
	artifactTTL := m.deps.ArtifactTTL
	if runTTL > 0 || artifactTTL > 0 {
		// Only pull runs old enough to possibly match *either* TTL — the
		// smaller of the two positive values is the earliest cutoff either
		// branch below could act on. ListTerminalBefore does this filtering
		// in SQL (indexed on (project_id, ended_at)) instead of loading every
		// run a project has ever had, terminal or not, expired or not.
		cutoffTTL := runTTL
		if artifactTTL > 0 && (cutoffTTL <= 0 || artifactTTL < cutoffTTL) {
			cutoffTTL = artifactTTL
		}
		now := time.Now().UTC()
		cutoff := now.Add(-cutoffTTL)
		projects, err := m.deps.ProjectRepo.List(ctx)
		if err != nil {
			slog.Warn("retention list projects failed", "err", err)
		}
		for _, projectRecord := range projects {
			runs, err := m.deps.RunRepo.ListTerminalBefore(ctx, projectRecord.ID, cutoff)
			if err != nil {
				slog.Warn("retention list terminal runs failed", "project_id", projectRecord.ID, "err", err)
				continue
			}
			for _, r := range runs {
				if runTTL > 0 && r.EndedAt.Before(now.Add(-runTTL)) {
					if err := m.DeleteRunWithArtifacts(project.WithContext(ctx, project.Context{ID: r.ProjectID}), r.ID); err != nil {
						slog.Warn("retention delete run failed", "run_id", r.ID, "err", err)
					}
					continue
				}
				if artifactTTL > 0 && r.EndedAt.Before(now.Add(-artifactTTL)) {
					// Store only: artifactTTL retires the artifact repository
					// copy, not the run's own workspace/record — that's
					// runTTL's job, above (fed.md §13.6).
					if err := m.deps.DeleteArtifacts(ctx, m.deps.Store, r.ID); err != nil {
						slog.Warn("retention delete artifacts failed", "run_id", r.ID, "err", err)
					}
				}
			}
		}
	}
	m.CleanupScheduleRetention(ctx)
}

// CleanupScheduleRetention enforces per-schedule max_runs retention, batched.
func (m *Manager) CleanupScheduleRetention(ctx context.Context) {
	schedules, err := m.deps.ScheduleRepo.ListWithMaxRuns(ctx)
	if err != nil {
		slog.Warn("retention list schedules with max_runs failed", "err", err)
		return
	}
	for _, sc := range schedules {
		// List returns runs newest-first (started_at DESC); we keep the first
		// max_runs terminal runs and delete the remainder — a non-terminal run
		// doesn't consume a "kept" slot, exactly as before. The fetch is now
		// bounded to max_runs+retentionScheduleBatch instead of the schedule's
		// entire run history: if the kept quota isn't reached within that
		// window (only possible with an implausible number of non-terminal
		// runs interspersed among the newest rows), this cycle simply deletes
		// nothing for this schedule rather than risk treating an uncounted
		// run as overflow — safe to pick up next cycle.
		runs, err := m.deps.RunRepo.List(ctx, sc.ProjectID, run.RunFilter{
			ScheduleID: sc.ID,
			Limit:      sc.MaxRuns + retentionScheduleBatch,
		})
		if err != nil {
			slog.Warn("retention list schedule runs failed", "project_id", sc.ProjectID, "schedule_id", sc.ID, "err", err)
			continue
		}
		kept := 0
		deleteIDs := make([]string, 0)
		for _, r := range runs {
			if r.EndedAt == nil || r.Status == run.StatusRunning || r.Status == run.StatusScheduled {
				continue
			}
			if kept < sc.MaxRuns {
				kept++
				continue
			}
			deleteIDs = append(deleteIDs, r.ID)
		}
		if len(deleteIDs) > 0 {
			if err := m.DeleteRunsWithArtifacts(project.WithContext(ctx, project.Context{ID: sc.ProjectID}), deleteIDs); err != nil {
				slog.Warn("retention delete schedule runs failed", "project_id", sc.ProjectID, "schedule_id", sc.ID, "count", len(deleteIDs), "err", err)
			}
		}
	}
}
