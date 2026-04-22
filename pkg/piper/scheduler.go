package piper

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/store"
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

func (p *Piper) runScheduler() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().UTC()
		due, err := p.store.ListDueSchedules(now)
		if err != nil {
			slog.Warn("list due schedules failed", "err", err)
		} else {
			for _, sc := range due {
				p.triggerSchedule(sc)
			}
		}

		dueRuns, err := p.store.ListDueScheduledRuns(now)
		if err != nil {
			slog.Warn("list due one-time runs failed", "err", err)
			continue
		}
		for _, run := range dueRuns {
			p.triggerScheduledRun(run)
		}
	}
}

func (p *Piper) triggerScheduledRun(run *store.Run) {
	pl, err := p.Parse([]byte(run.PipelineYAML))
	if err != nil {
		slog.Warn("parse one-time scheduled pipeline failed", "run_id", run.ID, "err", err)
		now := time.Now().UTC()
		_ = p.store.UpdateRunStatus(run.ID, "failed", &now)
		return
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		slog.Warn("build one-time scheduled dag failed", "run_id", run.ID, "err", err)
		now := time.Now().UTC()
		_ = p.store.UpdateRunStatus(run.ID, "failed", &now)
		return
	}

	startAt := time.Now().UTC()
	if err := p.store.MarkRunRunning(run.ID, startAt); err != nil {
		slog.Warn("mark run running failed", "run_id", run.ID, "err", err)
		return
	}

	outputDir := filepath.Join(p.cfg.OutputDir, run.ID)
	vars := proto.BuiltinVars{ScheduledAt: run.ScheduledAt}
	p.queue.add(pl, dag, run.ID, ".", outputDir, vars, nil)

	if p.cfg.Hooks.OnRunStart != nil {
		go p.cfg.Hooks.OnRunStart(context.Background(), run.ID, pl)
	}
}

func (p *Piper) triggerSchedule(sc *store.Schedule) {
	now := time.Now().UTC()
	nextRunAt, err := nextScheduleTime(sc.CronExpr, sc.NextRunAt)
	if err != nil {
		slog.Warn("invalid cron expression in schedule", "schedule_id", sc.ID, "cron", sc.CronExpr, "err", err)
		_ = p.store.SetScheduleEnabled(sc.ID, false)
		return
	}

	pl, err := p.Parse([]byte(sc.PipelineYAML))
	if err != nil {
		slog.Warn("parse schedule pipeline failed", "schedule_id", sc.ID, "err", err)
		_ = p.store.UpdateScheduleRun(sc.ID, now, nextRunAt)
		return
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		slog.Warn("build schedule dag failed", "schedule_id", sc.ID, "err", err)
		_ = p.store.UpdateScheduleRun(sc.ID, now, nextRunAt)
		return
	}

	runID := genRunID()
	outputDir := filepath.Join(p.cfg.OutputDir, runID)
	scheduledAt := sc.NextRunAt

	run := &store.Run{
		ID:           runID,
		OwnerID:      sc.OwnerID,
		PipelineName: pl.Metadata.Name,
		Status:       "running",
		StartedAt:    now,
		ScheduledAt:  &scheduledAt,
		PipelineYAML: sc.PipelineYAML,
	}
	if err := p.store.CreateRun(run); err != nil {
		slog.Warn("create run from schedule failed", "schedule_id", sc.ID, "err", err)
		return
	}

	for _, s := range pl.Spec.Steps {
		if err := p.store.UpsertStep(&store.Step{RunID: runID, StepName: s.Name, Status: "pending"}); err != nil {
			slog.Warn("init scheduled step failed", "run_id", runID, "step", s.Name, "err", err)
		}
	}

	var params map[string]any
	if sc.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(sc.ParamsJSON), &params)
	}
	p.queue.add(pl, dag, runID, ".", outputDir, proto.BuiltinVars{ScheduledAt: &scheduledAt}, params)
	if err := p.store.UpdateScheduleRun(sc.ID, now, nextRunAt); err != nil {
		slog.Warn("update schedule run failed", "schedule_id", sc.ID, "err", err)
	}
}
