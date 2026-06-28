package piper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/schedule"
	"github.com/robfig/cron/v3"
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func nextScheduleTime(expr string, from time.Time) (time.Time, error) {
	s, err := cronParser.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	return s.Next(from.UTC()), nil
}

func (p *Piper) BackfillSchedule(ctx context.Context, id string, from, to time.Time) ([]string, error) {
	projectContext, _ := project.FromContext(ctx)
	sc, err := p.repos.Schedule.Get(ctx, projectContext.ID, id)
	if err != nil {
		return nil, err
	}
	if sc.ScheduleType != "cron" {
		return nil, fmt.Errorf("backfill requires a cron schedule")
	}
	cronSchedule, err := cronParser.Parse(sc.CronExpr)
	if err != nil {
		return nil, err
	}
	pl, err := pipeline.Parse([]byte(sc.PipelineYAML))
	if err != nil {
		return nil, err
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return nil, err
	}
	var params map[string]any
	if sc.ParamsJSON != "" {
		if err := json.Unmarshal([]byte(sc.ParamsJSON), &params); err != nil {
			return nil, err
		}
	}

	var runIDs []string
	for scheduledAt := cronSchedule.Next(from.UTC().Add(-time.Nanosecond)); !scheduledAt.After(to.UTC()); scheduledAt = cronSchedule.Next(scheduledAt) {
		if len(runIDs) >= 1000 {
			return nil, fmt.Errorf("backfill range creates more than 1000 runs")
		}
		intervalEnd := cronSchedule.Next(scheduledAt)
		runID, err := p.startRun(ctx, pl, dag, StartRunOptions{
			ProjectID:  sc.ProjectID,
			ScheduleID: sc.ID,
			Params:     params,
			Vars: proto.BuiltinVars{
				ScheduledAt:     &scheduledAt,
				DataIntervalEnd: &intervalEnd,
			},
			YAML: sc.PipelineYAML,
		})
		if err != nil {
			return nil, err
		}
		runIDs = append(runIDs, runID)
	}
	return runIDs, nil
}

// scheduleFired is the FireFunc passed to the in-memory Scheduler.
// Reads fresh state from DB on every call so the Scheduler holds no sc copies.
func (p *Piper) scheduleFired(ctx context.Context, projectID, scheduleID string) {
	sc, err := p.repos.Schedule.Get(ctx, projectID, scheduleID)
	if err != nil || sc == nil {
		slog.Warn("scheduler: schedule not found", "id", scheduleID, "err", err)
		return
	}
	if !sc.Enabled {
		slog.Info("scheduler: stale callback ignored for disabled schedule", "id", scheduleID)
		return
	}

	now := time.Now().UTC()
	slog.Info("scheduler: fired", "schedule_id", sc.ID, "type", sc.ScheduleType, "planned_at", sc.NextRunAt, "now", now)

	switch sc.ScheduleType {
	case "cron":
		plannedAt := sc.NextRunAt
		next, err := nextScheduleTime(sc.CronExpr, now)
		if err != nil {
			slog.Warn("scheduler: invalid cron expression, disabling", "id", scheduleID, "err", err)
			_ = p.repos.Schedule.SetEnabled(ctx, projectID, scheduleID, false)
			p.scheduler.Remove(scheduleID)
			return
		}

		// Misfire: planned tick predates this process start — decide fire or skip.
		if plannedAt.Before(p.startedAt) {
			switch p.cfg.Schedule.MisfirePolicy {
			case "skip":
				slog.Info("scheduler: misfire skipped (policy=skip)", "id", scheduleID, "planned_at", plannedAt)
				if ok, err := p.repos.Schedule.AdvanceNextRun(ctx, projectID, scheduleID, plannedAt, next); !ok || err != nil {
					slog.Warn("scheduler: AdvanceNextRun failed or lost race", "id", scheduleID, "err", err)
				}
				return
			default: // run_once
				grace := p.cfg.Schedule.MisfireGracePeriod
				if grace > 0 && now.Sub(plannedAt) > grace {
					slog.Info("scheduler: misfire outside grace period, skipping", "id", scheduleID, "planned_at", plannedAt, "grace", grace)
					if ok, err := p.repos.Schedule.AdvanceNextRun(ctx, projectID, scheduleID, plannedAt, next); !ok || err != nil {
						slog.Warn("scheduler: AdvanceNextRun failed or lost race", "id", scheduleID, "err", err)
					}
					return
				}
			}
		}
		if plannedAt.After(now) {
			slog.Info("scheduler: stale callback ignored for future tick", "id", scheduleID, "planned_at", plannedAt, "now", now)
			return
		}

		// Atomic claim: disabled/deleted/modified/already-claimed → false
		claimed, err := p.repos.Schedule.ClaimRun(ctx, projectID, scheduleID, plannedAt, now, next)
		if err != nil {
			slog.Warn("scheduler: ClaimRun error", "id", scheduleID, "err", err)
			return
		}
		if !claimed {
			slog.Info("scheduler: tick already claimed or schedule changed, skipping", "id", scheduleID, "planned_at", plannedAt)
			return
		}

		p.fireSchedule(ctx, sc, proto.BuiltinVars{
			ScheduledAt:     &plannedAt,
			DataIntervalEnd: &next,
		})

	case "once", "immediate":
		scheduledAt := sc.NextRunAt
		claimed, err := p.repos.Schedule.ClaimOneShotRun(ctx, projectID, scheduleID, scheduledAt, now)
		if err != nil {
			slog.Warn("scheduler: ClaimOneShotRun error", "id", scheduleID, "err", err)
			return
		}
		if !claimed {
			slog.Info("scheduler: one-shot already claimed or disabled, skipping", "id", scheduleID)
			return
		}
		p.fireSchedule(ctx, sc, proto.BuiltinVars{ScheduledAt: &scheduledAt})
	}
}

// fireSchedule parses the pipeline and starts the run. Called from a goroutine.
func (p *Piper) fireSchedule(ctx context.Context, sc *schedule.Schedule, vars proto.BuiltinVars) {
	pl, err := p.Parse([]byte(sc.PipelineYAML))
	if err != nil {
		slog.Warn("parse schedule pipeline failed", "schedule_id", sc.ID, "err", err)
		return
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		slog.Warn("build schedule dag failed", "schedule_id", sc.ID, "err", err)
		return
	}

	var params map[string]any
	if sc.ParamsJSON != "" {
		if err := json.Unmarshal([]byte(sc.ParamsJSON), &params); err != nil {
			slog.Warn("parse schedule params failed", "schedule_id", sc.ID, "err", err)
		}
	}

	opts := StartRunOptions{
		ProjectID:  sc.ProjectID,
		ScheduleID: sc.ID,
		Params:     params,
		Vars:       vars,
		YAML:       sc.PipelineYAML,
	}
	delays := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		if attempt > 0 {
			time.Sleep(delays[attempt-1])
			slog.Info("scheduler: retrying run creation", "schedule_id", sc.ID, "attempt", attempt+1)
		}
		if _, err := p.startRun(ctx, pl, dag, opts); err != nil {
			lastErr = err
			continue
		}
		return
	}
	slog.Error("scheduler: run creation failed after retries", "schedule_id", sc.ID, "err", lastErr)
}
