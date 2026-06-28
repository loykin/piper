package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/piper/piper/pkg/schedule"
	"github.com/robfig/cron/v3"
)

// schedEntry tracks the in-memory state for a registered schedule.
// cronID is needed to remove the correct job from the cron runner on reload/remove.
type schedEntry struct {
	projectID string
	cronID    cron.EntryID
}

// Scheduler manages cron and one-shot schedules in memory.
// It is a pure alarm index: it stores only the identifiers and timer/cron handle,
// and calls FireFunc with (projectID, scheduleID) — all state lives in the DB.
// Implements schedule.SchedulerAPI.
type Scheduler struct {
	cr      *cron.Cron
	entries map[string]schedEntry // scheduleID → entry (cron type)
	timers  map[string]*time.Timer
	mu      sync.Mutex
	fire    schedule.FireFunc
	ctx     context.Context
	cancel  context.CancelFunc
}

// New creates a Scheduler. Call Start before adding schedules.
func New(fire schedule.FireFunc) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		cr:      cron.New(),
		entries: make(map[string]schedEntry),
		timers:  make(map[string]*time.Timer),
		fire:    fire,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start begins the cron runner. Must be called once before Add.
func (s *Scheduler) Start() {
	s.cr.Start()
}

// Stop halts all schedules and cancels the context passed to FireFunc.
func (s *Scheduler) Stop() {
	s.cancel()
	s.cr.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, t := range s.timers {
		t.Stop()
		delete(s.timers, id)
	}
}

// Add registers a schedule. If the schedule ID is already registered it is
// atomically replaced (reload). Safe to call concurrently.
// For cron type, if next_run_at is already in the past, fires once immediately
// before registering the recurring job — DB-side logic decides fire/skip.
func (s *Scheduler) Add(sc *schedule.Schedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(sc.ID)

	switch sc.ScheduleType {
	case "cron":
		if !sc.NextRunAt.IsZero() && sc.NextRunAt.Before(time.Now()) {
			projectID, scheduleID := sc.ProjectID, sc.ID
			go s.fire(s.ctx, projectID, scheduleID)
		}
		projectID, scheduleID := sc.ProjectID, sc.ID
		id, err := s.cr.AddFunc(sc.CronExpr, func() {
			s.fire(s.ctx, projectID, scheduleID)
		})
		if err != nil {
			return err
		}
		s.entries[sc.ID] = schedEntry{projectID: sc.ProjectID, cronID: id}
		slog.Info("scheduler: cron registered", "schedule_id", sc.ID, "expr", sc.CronExpr, "next_run_at", sc.NextRunAt)

	case "once":
		delay := time.Until(sc.NextRunAt)
		if delay < 0 {
			delay = 0
		}
		projectID, scheduleID := sc.ProjectID, sc.ID
		t := time.AfterFunc(delay, func() {
			s.mu.Lock()
			delete(s.timers, scheduleID)
			s.mu.Unlock()
			s.fire(s.ctx, projectID, scheduleID)
		})
		s.timers[sc.ID] = t

	case "immediate":
		projectID, scheduleID := sc.ProjectID, sc.ID
		go s.fire(s.ctx, projectID, scheduleID)
	}

	return nil
}

// Remove unregisters a schedule. No-op if the ID is not registered.
func (s *Scheduler) Remove(scheduleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(scheduleID)
}

func (s *Scheduler) removeLocked(id string) {
	if e, ok := s.entries[id]; ok {
		s.cr.Remove(e.cronID)
		delete(s.entries, id)
	}
	if t, ok := s.timers[id]; ok {
		t.Stop()
		delete(s.timers, id)
	}
}

// LoadFromRepo seeds s from the repository.
// Call this after Start() during server initialisation.
func LoadFromRepo(ctx context.Context, repo schedule.Repository, s *Scheduler) error {
	schedules, err := repo.ListEnabled(ctx)
	if err != nil {
		return err
	}
	slog.Info("scheduler: loading schedules from repo", "count", len(schedules))
	for _, sc := range schedules {
		if err := s.Add(sc); err != nil {
			slog.Warn("scheduler: failed to register schedule", "schedule_id", sc.ID, "err", err)
		}
	}
	return nil
}
