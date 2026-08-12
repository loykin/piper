package queue

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

// newSQLiteQueueFixture seeds a run row the way production does
// (piper.go's SubmitRun: Project.Create then Run.Create with
// Status=StatusRunning, *before* Queue.AddWithEnv) so these tests exercise
// the real FinalizeStatusCAS/UpsertCAS SQL guards Queue's CAS orchestration
// (transitionTaskLocked, finalizeRunLocked) depends on, not just the
// hand-rolled memoryRunRepo/memoryStepRepo fakes used elsewhere in this
// package's tests.
func newSQLiteQueueFixture(t *testing.T, runID string) (*Queue, *store.Repos) {
	t.Helper()
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	ctx := context.Background()
	if err := repos.Project.Create(ctx, &project.Project{ID: "project-a", Name: "project-a"}); err != nil {
		t.Fatalf("Project.Create: %v", err)
	}
	if err := repos.Run.Create(ctx, &run.Run{
		ID:           runID,
		ProjectID:    "project-a",
		PipelineName: runID,
		Status:       run.StatusRunning,
		StartedAt:    time.Now(),
	}); err != nil {
		t.Fatalf("Run.Create: %v", err)
	}

	q := NewQueue(ctx, repos.Run, repos.Step)
	t.Cleanup(func() { _ = q.Close(context.Background()) })
	return q, repos
}

// TestQueueCompleteFinalizesRunAgainstRealSQLite freezes fed.md 13.1's
// "Start and terminal completion" behavior end-to-end against a real
// repository: proves the real FinalizeStatusCAS UPDATE actually fires from
// Queue's real call site, not just in an isolated repotest-suite assertion.
func TestQueueCompleteFinalizesRunAgainstRealSQLite(t *testing.T) {
	ctx := context.Background()
	runID := "run-sqlite-complete"
	q, repos := newSQLiteQueueFixture(t, runID)

	pl := singleStepPipeline("sqlite-complete")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q.Add(ctx, "project-a", pl, dag, runID, ".", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected a ready task")
	}
	now := time.Now()
	if err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if !waitUntil(2*time.Second, func() bool {
		r, err := repos.Run.Get(ctx, "project-a", runID)
		return err == nil && r != nil && r.Status == run.StatusSuccess
	}) {
		t.Fatal("run did not reach success status in real SQLite")
	}
	steps, err := repos.Step.List(ctx, "project-a", runID)
	if err != nil {
		t.Fatalf("Step.List: %v", err)
	}
	if len(steps) != 1 || steps[0].Status != "done" {
		t.Fatalf("steps = %#v, want 1 step with status done", steps)
	}
}

// TestQueueCancelAgainstRealSQLite freezes fed.md 13.1's "cancel while
// running" behavior against a real repository, mirroring
// TestCancelRemovesQueuedRunAndMarksStepsCanceled's fake-backed scenario.
func TestQueueCancelAgainstRealSQLite(t *testing.T) {
	ctx := context.Background()
	runID := "run-sqlite-cancel"
	q, repos := newSQLiteQueueFixture(t, runID)

	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "sqlite-cancel"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"sleep", "60"}}},
			{Name: "second", Run: pipeline.Run{Command: []string{"echo", "second"}}, DependsOn: []string{"first"}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	taskBackend := &cancelRecordingBackend{}
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, runID, ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 1
	}) {
		t.Fatal("first task was not dispatched")
	}
	if err := q.Cancel(ctx, "project-a", runID); err != nil {
		t.Fatal(err)
	}

	if !waitUntil(2*time.Second, func() bool {
		r, err := repos.Run.Get(ctx, "project-a", runID)
		return err == nil && r != nil && r.Status == run.StatusCanceled
	}) {
		t.Fatal("run did not reach canceled status in real SQLite")
	}
	steps, err := repos.Step.List(ctx, "project-a", runID)
	if err != nil {
		t.Fatalf("Step.List: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %#v, want 2", steps)
	}
	for _, s := range steps {
		if s.Status != "canceled" {
			t.Fatalf("step %q status = %q, want canceled", s.StepName, s.Status)
		}
	}
}

// TestQueueDuplicateStaleFutureResultAgainstRealSQLite freezes fed.md 13.1's
// "duplicate, stale, and future result rejection" behavior against a real
// repository — the core CAS-integration gap: proves the real
// UpsertCAS/FinalizeStatusCAS SQL guards reject a stale/future attempt and
// no-op a duplicate the same way the hand-rolled fakes do (see
// TestCompleteDuplicateResultIsIgnored, TestCompleteStaleAttemptIsIgnored,
// TestCompleteFutureAttemptIsRejected).
func TestQueueDuplicateStaleFutureResultAgainstRealSQLite(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		ctx := context.Background()
		runID := "run-sqlite-dup"
		q, repos := newSQLiteQueueFixture(t, runID)

		pl := singleStepPipeline("sqlite-dup")
		dag, _ := pipeline.BuildDAG(pl)
		q.Add(ctx, "project-a", pl, dag, runID, ".", t.TempDir(), proto.BuiltinVars{}, nil)
		task := q.takeReadyTask("", "")
		if task == nil {
			t.Fatal("expected a ready task")
		}
		now := time.Now()
		result := proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}
		if err := q.Complete(ctx, result); err != nil {
			t.Fatalf("first complete: %v", err)
		}
		if err := q.Complete(ctx, result); err != nil {
			t.Fatalf("duplicate complete returned error: %v", err)
		}
		if !waitUntil(2*time.Second, func() bool {
			r, err := repos.Run.Get(ctx, "project-a", runID)
			return err == nil && r != nil && r.Status == run.StatusSuccess
		}) {
			t.Fatal("run did not reach success status in real SQLite")
		}
	})

	t.Run("stale", func(t *testing.T) {
		ctx := context.Background()
		runID := "run-sqlite-stale"
		q, _ := newSQLiteQueueFixture(t, runID)
		q.SetRetryPolicy(2, 0)

		pl := singleStepPipeline("sqlite-stale")
		dag, _ := pipeline.BuildDAG(pl)
		q.Add(ctx, "project-a", pl, dag, runID, ".", t.TempDir(), proto.BuiltinVars{}, nil)
		task := q.takeReadyTask("", "")
		if task == nil {
			t.Fatal("expected a ready task")
		}
		now := time.Now()
		// fail attempt 1 -> queue retries
		if err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusFailed, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
			t.Fatalf("fail attempt 1: %v", err)
		}
		task2 := q.takeReadyTask("", "")
		if task2 == nil {
			t.Fatal("no retry task available")
		}
		// stale result from attempt 1 arriving after attempt 2 started must be ignored
		if err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
			t.Fatalf("stale attempt complete returned error: %v", err)
		}
		if s := q.Stats(); s.Running != 1 {
			t.Fatalf("stats = %+v, want task still running after stale result", s)
		}
	})

	t.Run("future", func(t *testing.T) {
		ctx := context.Background()
		runID := "run-sqlite-future"
		q, _ := newSQLiteQueueFixture(t, runID)

		pl := singleStepPipeline("sqlite-future")
		dag, _ := pipeline.BuildDAG(pl)
		q.Add(ctx, "project-a", pl, dag, runID, ".", t.TempDir(), proto.BuiltinVars{}, nil)
		task := q.takeReadyTask("", "")
		if task == nil {
			t.Fatal("expected a ready task")
		}
		now := time.Now()
		err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 99})
		if err == nil {
			t.Fatal("future attempt was accepted, expected error")
		}
	})
}
