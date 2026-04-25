package piper

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
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

func (p *Piper) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			due, err := p.repos.Schedule.ListDue(p.ctx, now)
			if err != nil {
				slog.Warn("list due schedules failed", "err", err)
				continue
			}
			for _, sc := range due {
				go p.triggerSchedule(ctx, sc)
			}
		}
	}
}

func (p *Piper) triggerSchedule(ctx context.Context, sc *schedule.Schedule) {
	now := time.Now().UTC()

	var nextRunAt time.Time
	if sc.ScheduleType == "cron" {
		var err error
		nextRunAt, err = nextScheduleTime(sc.CronExpr, sc.NextRunAt)
		if err != nil {
			slog.Warn("invalid cron expression in schedule", "schedule_id", sc.ID, "cron", sc.CronExpr, "err", err)
			_ = p.repos.Schedule.SetEnabled(ctx, sc.ID, false)
			return
		}
	}

	pl, err := p.Parse([]byte(sc.PipelineYAML))
	if err != nil {
		slog.Warn("parse schedule pipeline failed", "schedule_id", sc.ID, "err", err)
		if sc.ScheduleType == "cron" {
			_ = p.repos.Schedule.UpdateRun(ctx, sc.ID, now, nextRunAt)
		}
		return
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		slog.Warn("build schedule dag failed", "schedule_id", sc.ID, "err", err)
		if sc.ScheduleType == "cron" {
			_ = p.repos.Schedule.UpdateRun(ctx, sc.ID, now, nextRunAt)
		}
		return
	}

	scheduledAt := sc.NextRunAt

	var params map[string]any
	if sc.ParamsJSON != "" {
		if err := json.Unmarshal([]byte(sc.ParamsJSON), &params); err != nil {
			slog.Warn("parse schedule params failed", "schedule_id", sc.ID, "err", err)
		}
	}

	if _, err := p.startRun(ctx, pl, dag, StartRunOptions{
		ScheduleID: sc.ID,
		OwnerID:    sc.OwnerID,
		Params:     params,
		Vars:       proto.BuiltinVars{ScheduledAt: &scheduledAt},
		YAML:       sc.PipelineYAML,
	}); err != nil {
		slog.Warn("create run from schedule failed", "schedule_id", sc.ID, "err", err)
		return
	}

	switch sc.ScheduleType {
	case "cron":
		if err := p.repos.Schedule.UpdateRun(ctx, sc.ID, now, nextRunAt); err != nil {
			slog.Warn("update schedule run failed", "schedule_id", sc.ID, "err", err)
		}
	case "once", "immediate":
		// fire once then done
		if err := p.repos.Schedule.SetEnabled(ctx, sc.ID, false); err != nil {
			slog.Warn("mark schedule done failed", "schedule_id", sc.ID, "err", err)
		}
		_ = p.repos.Schedule.UpdateRun(ctx, sc.ID, now, now)
	}
}
