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
			continue
		}
		for _, sc := range due {
			p.triggerSchedule(sc)
		}
	}
}

func (p *Piper) triggerSchedule(sc *store.Schedule) {
	now := time.Now().UTC()

	var nextRunAt time.Time
	if sc.ScheduleType == "cron" {
		var err error
		nextRunAt, err = nextScheduleTime(sc.CronExpr, sc.NextRunAt)
		if err != nil {
			slog.Warn("invalid cron expression in schedule", "schedule_id", sc.ID, "cron", sc.CronExpr, "err", err)
			_ = p.store.SetScheduleEnabled(sc.ID, false)
			return
		}
	}

	pl, err := p.Parse([]byte(sc.PipelineYAML))
	if err != nil {
		slog.Warn("parse schedule pipeline failed", "schedule_id", sc.ID, "err", err)
		if sc.ScheduleType == "cron" {
			_ = p.store.UpdateScheduleRun(sc.ID, now, nextRunAt)
		}
		return
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		slog.Warn("build schedule dag failed", "schedule_id", sc.ID, "err", err)
		if sc.ScheduleType == "cron" {
			_ = p.store.UpdateScheduleRun(sc.ID, now, nextRunAt)
		}
		return
	}

	runID := genRunID()
	outputDir := filepath.Join(p.cfg.OutputDir, runID)
	scheduledAt := sc.NextRunAt

	run := &store.Run{
		ID:           runID,
		ScheduleID:   sc.ID,
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

	if p.cfg.Hooks.OnRunStart != nil {
		go p.cfg.Hooks.OnRunStart(context.Background(), runID, pl)
	}

	switch sc.ScheduleType {
	case "cron":
		if err := p.store.UpdateScheduleRun(sc.ID, now, nextRunAt); err != nil {
			slog.Warn("update schedule run failed", "schedule_id", sc.ID, "err", err)
		}
	case "once", "immediate":
		// fire once then done
		if err := p.store.SetScheduleEnabled(sc.ID, false); err != nil {
			slog.Warn("mark schedule done failed", "schedule_id", sc.ID, "err", err)
		}
		_ = p.store.UpdateScheduleRun(sc.ID, now, now)
	}
}
