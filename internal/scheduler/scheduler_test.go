package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piper/piper/internal/scheduler"
	"github.com/piper/piper/pkg/schedule"
)

func newTestScheduler(t *testing.T, fire schedule.FireFunc) *scheduler.Scheduler {
	t.Helper()
	s := scheduler.New(fire)
	s.Start()
	t.Cleanup(s.Stop)
	return s
}

func TestCron_Fires(t *testing.T) {
	t.Parallel()
	fired := make(chan struct{}, 10)
	s := newTestScheduler(t, func(_ context.Context, _, _ string) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	err := s.Add(&schedule.Schedule{ID: "c1", ScheduleType: "cron", CronExpr: "@every 50ms"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	timeout := time.After(3 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-fired:
		case <-timeout:
			t.Fatalf("cron fired only %d time(s), expected at least 2", i)
		}
	}
}

func TestCron_InvalidExpr(t *testing.T) {
	t.Parallel()
	s := newTestScheduler(t, func(_ context.Context, _, _ string) {})
	err := s.Add(&schedule.Schedule{ID: "bad", ScheduleType: "cron", CronExpr: "not-valid"})
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestOnce_FiresExactlyOnce(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	s := newTestScheduler(t, func(_ context.Context, _, _ string) { count.Add(1) })

	err := s.Add(&schedule.Schedule{
		ID:           "o1",
		ScheduleType: "once",
		NextRunAt:    time.Now().Add(40 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := count.Load(); n != 1 {
		t.Errorf("expected exactly 1 fire, got %d", n)
	}
}

func TestOnce_PastDue(t *testing.T) {
	t.Parallel()
	fired := make(chan struct{}, 1)
	s := newTestScheduler(t, func(_ context.Context, _, _ string) { fired <- struct{}{} })

	err := s.Add(&schedule.Schedule{
		ID:           "o2",
		ScheduleType: "once",
		NextRunAt:    time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-fired:
	case <-time.After(500 * time.Millisecond):
		t.Error("past-due once schedule did not fire immediately")
	}
}

func TestImmediate_Fires(t *testing.T) {
	t.Parallel()
	fired := make(chan struct{}, 1)
	s := newTestScheduler(t, func(_ context.Context, _, _ string) { fired <- struct{}{} })

	err := s.Add(&schedule.Schedule{ID: "i1", ScheduleType: "immediate"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-fired:
	case <-time.After(500 * time.Millisecond):
		t.Error("immediate schedule did not fire")
	}
}

func TestRemove_StopsCron(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	s := newTestScheduler(t, func(_ context.Context, _, _ string) { count.Add(1) })

	err := s.Add(&schedule.Schedule{ID: "r1", ScheduleType: "cron", CronExpr: "@every 30ms"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	s.Remove("r1")
	snapshot := count.Load()
	time.Sleep(100 * time.Millisecond)
	if after := count.Load(); after != snapshot {
		t.Errorf("fired %d times after Remove (expected 0)", after-snapshot)
	}
}

func TestRemove_StopsOnce(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	s := newTestScheduler(t, func(_ context.Context, _, _ string) { count.Add(1) })

	err := s.Add(&schedule.Schedule{
		ID:           "r2",
		ScheduleType: "once",
		NextRunAt:    time.Now().Add(200 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Remove("r2")
	time.Sleep(400 * time.Millisecond)
	if n := count.Load(); n != 0 {
		t.Errorf("expected 0 fires after Remove, got %d", n)
	}
}

func TestReload_ReplacesCron(t *testing.T) {
	t.Parallel()
	fired := make(chan struct{}, 10)

	s := newTestScheduler(t, func(_ context.Context, _, _ string) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	// slow 10s cron; zero NextRunAt so no immediate fire
	slow := &schedule.Schedule{ID: "rl1", ScheduleType: "cron", CronExpr: "@every 10s"}
	if err := s.Add(slow); err != nil {
		t.Fatalf("Add slow: %v", err)
	}
	// replace with fast 40ms cron
	fast := &schedule.Schedule{ID: "rl1", ScheduleType: "cron", CronExpr: "@every 40ms"}
	if err := s.Add(fast); err != nil {
		t.Fatalf("Add fast: %v", err)
	}

	// fast (@every 40ms) should fire within 3s; slow (10s) cannot fire in that window
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Error("no fires after reload to fast cron — slow cron may not have been replaced")
	}
}

func TestReload_CronToOnce(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	s := newTestScheduler(t, func(_ context.Context, _, _ string) { count.Add(1) })

	if err := s.Add(&schedule.Schedule{ID: "rl2", ScheduleType: "cron", CronExpr: "@every 10s"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(&schedule.Schedule{
		ID:           "rl2",
		ScheduleType: "once",
		NextRunAt:    time.Now().Add(50 * time.Millisecond),
	}); err != nil {
		t.Fatalf("Add once: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := count.Load(); n != 1 {
		t.Errorf("expected exactly 1 fire after cron→once reload, got %d", n)
	}
}

func TestStop_HaltsAll(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	s := scheduler.New(func(_ context.Context, _, _ string) { count.Add(1) })
	s.Start()

	if err := s.Add(&schedule.Schedule{ID: "st1", ScheduleType: "cron", CronExpr: "@every 30ms"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	s.Stop()
	snapshot := count.Load()
	time.Sleep(100 * time.Millisecond)
	if after := count.Load(); after != snapshot {
		t.Errorf("fired %d times after Stop", after-snapshot)
	}
}

func TestContext_CancelledOnStop(t *testing.T) {
	t.Parallel()
	ctxCh := make(chan context.Context, 1)
	s := scheduler.New(func(ctx context.Context, _, _ string) {
		select {
		case ctxCh <- ctx:
		default:
		}
	})
	s.Start()

	if err := s.Add(&schedule.Schedule{ID: "ctx1", ScheduleType: "cron", CronExpr: "@every 30ms"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var ctx context.Context
	select {
	case ctx = <-ctxCh:
	case <-time.After(3 * time.Second):
		t.Fatal("schedule did not fire")
	}
	s.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Error("context was not cancelled after Stop")
	}
}

func TestRemoveNoop(t *testing.T) {
	t.Parallel()
	s := newTestScheduler(t, func(_ context.Context, _, _ string) {})
	s.Remove("does-not-exist")
}

func TestCronMisfireRecovery(t *testing.T) {
	t.Parallel()
	fired := make(chan struct{}, 1)
	s := newTestScheduler(t, func(_ context.Context, _, _ string) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	// next_run_at is 1 hour in the past → misfire
	err := s.Add(&schedule.Schedule{
		ID:           "misfire1",
		ScheduleType: "cron",
		CronExpr:     "@every 10s",
		NextRunAt:    time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	select {
	case <-fired:
	case <-time.After(500 * time.Millisecond):
		t.Error("cron misfire did not fire immediately")
	}
}

func TestFire_PassesProjectIDAndScheduleID(t *testing.T) {
	t.Parallel()
	type call struct{ projectID, scheduleID string }
	calls := make(chan call, 2)

	s := newTestScheduler(t, func(_ context.Context, projectID, scheduleID string) {
		select {
		case calls <- call{projectID, scheduleID}:
		default:
		}
	})

	err := s.Add(&schedule.Schedule{
		ID:           "sched-1",
		ProjectID:    "proj-1",
		ScheduleType: "immediate",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	select {
	case c := <-calls:
		if c.projectID != "proj-1" {
			t.Errorf("projectID = %q, want proj-1", c.projectID)
		}
		if c.scheduleID != "sched-1" {
			t.Errorf("scheduleID = %q, want sched-1", c.scheduleID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("schedule did not fire")
	}
}
