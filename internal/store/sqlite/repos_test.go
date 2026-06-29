package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/piper/piper/internal/store"
	"github.com/piper/piper/internal/store/repotest"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/schedule"
)

func TestRunRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	const projectID = "run-repo"
	if err := repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.RunRepoSuite(t, repos.Run, projectID)
}

func TestProjectRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	repotest.ProjectRepoSuite(t, repos.Project)
}

func TestStepRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	const projectID = "step-repo"
	if err := repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.StepRepoSuite(t, repos.Step, projectID)
}

func TestSecretRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	const projectID = "secret-repo"
	if err := repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.SecretRepoSuite(t, repos.Secret, projectID)
}

// TestScheduleTimeRoundTrip verifies that time.Time values stored via ClaimRun
// are correctly read back by ListEnabled. A failed round-trip would cause
// LoadFromRepo to see a wrong NextRunAt after server restart.
func TestScheduleTimeRoundTrip(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	if err := repos.Project.Create(ctx, &project.Project{ID: "p1", Name: "p1"}); err != nil {
		t.Fatal(err)
	}

	nextRunAt := time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC) // 20:00 KST
	sc := &schedule.Schedule{
		ProjectID: "p1", ID: "sch-rt", Name: "rt",
		ScheduleType: "cron", CronExpr: "0 * * * *",
		Enabled: true, NextRunAt: nextRunAt,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	enabled, err := repos.Schedule.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(enabled))
	}
	if !enabled[0].NextRunAt.Equal(nextRunAt) {
		t.Errorf("after Create: NextRunAt = %v, want %v", enabled[0].NextRunAt, nextRunAt)
	}

	// Simulate scheduleFired claiming a run and advancing next_run_at to the following hour.
	nextAfter := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC) // 21:00 KST
	claimed, err := repos.Schedule.ClaimRun(ctx, "p1", "sch-rt", nextRunAt, time.Now().UTC(), nextAfter)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if !claimed {
		t.Fatal("ClaimRun: expected to claim, got false")
	}

	enabled2, err := repos.Schedule.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled after ClaimRun: %v", err)
	}
	if len(enabled2) != 1 {
		t.Fatalf("expected 1 schedule after ClaimRun, got %d", len(enabled2))
	}
	if !enabled2[0].NextRunAt.Equal(nextAfter) {
		t.Errorf("after ClaimRun: NextRunAt = %v, want %v", enabled2[0].NextRunAt, nextAfter)
	}

	// Verify the loaded value is in the future relative to 2026-06-27T10:00Z (before 20:00 KST).
	simulatedRestartTime := time.Date(2026, 6, 27, 10, 27, 49, 0, time.UTC)
	if enabled2[0].NextRunAt.Before(simulatedRestartTime) {
		t.Errorf("NextRunAt %v should NOT be before restart time %v — misfire would be triggered incorrectly",
			enabled2[0].NextRunAt, simulatedRestartTime)
	}
}

func TestScheduleMaxRunsRoundTrip(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	if err := repos.Project.Create(ctx, &project.Project{ID: "p1", Name: "p1"}); err != nil {
		t.Fatal(err)
	}

	sc := &schedule.Schedule{
		ProjectID:    "p1",
		ID:           "sch-max",
		Name:         "max",
		ScheduleType: "cron",
		CronExpr:     "0 * * * *",
		Enabled:      true,
		MaxRuns:      3,
		NextRunAt:    time.Now().UTC().Add(time.Hour),
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repos.Schedule.Get(ctx, "p1", "sch-max")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MaxRuns != 3 {
		t.Fatalf("MaxRuns = %d, want 3", got.MaxRuns)
	}
}

func TestScheduleAtomicTransitions(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	const projectID = "schedule-atomic"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	createdAt := time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC)
	expectedAt := time.Date(2026, 6, 28, 1, 0, 0, 0, time.UTC)
	lastRunAt := time.Date(2026, 6, 28, 1, 0, 5, 0, time.UTC)
	nextRunAt := time.Date(2026, 6, 28, 2, 0, 0, 0, time.UTC)
	sc := &schedule.Schedule{
		ProjectID:    projectID,
		ID:           "sch-claim",
		Name:         "claim",
		ScheduleType: "cron",
		CronExpr:     "0 * * * *",
		Enabled:      true,
		NextRunAt:    expectedAt,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	if err := repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, err := repos.Schedule.ClaimRun(ctx, projectID, sc.ID, expectedAt, lastRunAt, nextRunAt)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if !claimed {
		t.Fatal("ClaimRun = false, want true")
	}
	got, err := repos.Schedule.Get(ctx, projectID, sc.ID)
	if err != nil {
		t.Fatalf("Get after ClaimRun: %v", err)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(lastRunAt) {
		t.Fatalf("LastRunAt = %v, want %v", got.LastRunAt, lastRunAt)
	}
	if !got.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("NextRunAt = %v, want %v", got.NextRunAt, nextRunAt)
	}

	claimed, err = repos.Schedule.ClaimRun(ctx, projectID, sc.ID, expectedAt, lastRunAt.Add(time.Minute), nextRunAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("ClaimRun stale expectedAt: %v", err)
	}
	if claimed {
		t.Fatal("ClaimRun stale expectedAt = true, want false")
	}
	got, err = repos.Schedule.Get(ctx, projectID, sc.ID)
	if err != nil {
		t.Fatalf("Get after stale ClaimRun: %v", err)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(lastRunAt) || !got.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("stale ClaimRun mutated schedule: last=%v next=%v", got.LastRunAt, got.NextRunAt)
	}

	if err := repos.Schedule.SetEnabled(ctx, projectID, sc.ID, false); err != nil {
		t.Fatalf("SetEnabled false: %v", err)
	}
	claimed, err = repos.Schedule.ClaimRun(ctx, projectID, sc.ID, nextRunAt, lastRunAt.Add(time.Minute), nextRunAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("ClaimRun disabled: %v", err)
	}
	if claimed {
		t.Fatal("ClaimRun disabled = true, want false")
	}
}

func TestScheduleAdvanceNextRunDoesNotTouchLastRunAt(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	const projectID = "schedule-advance"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	lastRunAt := time.Date(2026, 6, 28, 0, 55, 0, 0, time.UTC)
	expectedAt := time.Date(2026, 6, 28, 1, 0, 0, 0, time.UTC)
	nextRunAt := time.Date(2026, 6, 28, 2, 0, 0, 0, time.UTC)
	sc := &schedule.Schedule{
		ProjectID:    projectID,
		ID:           "sch-skip",
		Name:         "skip",
		ScheduleType: "cron",
		CronExpr:     "0 * * * *",
		Enabled:      true,
		LastRunAt:    &lastRunAt,
		NextRunAt:    expectedAt,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ok, err := repos.Schedule.AdvanceNextRun(ctx, projectID, sc.ID, expectedAt, nextRunAt)
	if err != nil {
		t.Fatalf("AdvanceNextRun: %v", err)
	}
	if !ok {
		t.Fatal("AdvanceNextRun = false, want true")
	}
	got, err := repos.Schedule.Get(ctx, projectID, sc.ID)
	if err != nil {
		t.Fatalf("Get after AdvanceNextRun: %v", err)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(lastRunAt) {
		t.Fatalf("LastRunAt = %v, want unchanged %v", got.LastRunAt, lastRunAt)
	}
	if !got.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("NextRunAt = %v, want %v", got.NextRunAt, nextRunAt)
	}

	ok, err = repos.Schedule.AdvanceNextRun(ctx, projectID, sc.ID, expectedAt, nextRunAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("AdvanceNextRun stale expectedAt: %v", err)
	}
	if ok {
		t.Fatal("AdvanceNextRun stale expectedAt = true, want false")
	}
}

func TestScheduleClaimOneShotRun(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	ctx := context.Background()
	const projectID = "schedule-oneshot"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	expectedAt := time.Date(2026, 6, 28, 1, 0, 0, 0, time.UTC)
	lastRunAt := time.Date(2026, 6, 28, 1, 0, 3, 0, time.UTC)
	sc := &schedule.Schedule{
		ProjectID:    projectID,
		ID:           "sch-once",
		Name:         "once",
		ScheduleType: "once",
		Enabled:      true,
		NextRunAt:    expectedAt,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := repos.Schedule.Create(ctx, sc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ok, err := repos.Schedule.ClaimOneShotRun(ctx, projectID, sc.ID, expectedAt, lastRunAt)
	if err != nil {
		t.Fatalf("ClaimOneShotRun: %v", err)
	}
	if !ok {
		t.Fatal("ClaimOneShotRun = false, want true")
	}
	got, err := repos.Schedule.Get(ctx, projectID, sc.ID)
	if err != nil {
		t.Fatalf("Get after ClaimOneShotRun: %v", err)
	}
	if got.Enabled {
		t.Fatal("Enabled = true, want false after one-shot claim")
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(lastRunAt) {
		t.Fatalf("LastRunAt = %v, want %v", got.LastRunAt, lastRunAt)
	}
	if !got.NextRunAt.Equal(lastRunAt) {
		t.Fatalf("NextRunAt = %v, want %v", got.NextRunAt, lastRunAt)
	}

	ok, err = repos.Schedule.ClaimOneShotRun(ctx, projectID, sc.ID, expectedAt, lastRunAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimOneShotRun second call: %v", err)
	}
	if ok {
		t.Fatal("ClaimOneShotRun second call = true, want false")
	}
}
