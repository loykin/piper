package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/run"
)

type memoryRunRepo struct {
	status map[string]string
}

func (r *memoryRunRepo) Create(context.Context, *run.Run) error { return nil }
func (r *memoryRunRepo) Get(_ context.Context, id string) (*run.Run, error) {
	if status, ok := r.status[id]; ok {
		return &run.Run{ID: id, Status: status}, nil
	}
	return nil, nil
}
func (r *memoryRunRepo) List(context.Context, run.RunFilter) ([]*run.Run, error) {
	return nil, nil
}
func (r *memoryRunRepo) UpdateStatus(_ context.Context, id, status string, _ *time.Time) error {
	if r.status == nil {
		r.status = map[string]string{}
	}
	r.status[id] = status
	return nil
}
func (r *memoryRunRepo) MarkRunning(context.Context, string, time.Time) error { return nil }
func (r *memoryRunRepo) Delete(context.Context, string) error                 { return nil }
func (r *memoryRunRepo) GetLatestSuccessful(context.Context, string) (*run.Run, error) {
	return nil, nil
}

type memoryStepRepo struct {
	steps map[string]*run.Step
}

func (r *memoryStepRepo) Upsert(_ context.Context, step *run.Step) error {
	if r.steps == nil {
		r.steps = map[string]*run.Step{}
	}
	cp := *step
	r.steps[step.RunID+":"+step.StepName] = &cp
	return nil
}
func (r *memoryStepRepo) List(context.Context, string) ([]*run.Step, error) {
	return nil, nil
}
func (r *memoryStepRepo) DeleteByRun(context.Context, string) error { return nil }

type recordingBackend struct {
	mu    sync.Mutex
	tasks []*proto.Task
}

func (b *recordingBackend) Dispatch(_ context.Context, task *proto.Task) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := *task
	b.tasks = append(b.tasks, &cp)
	return nil
}

func (b *recordingBackend) snapshot() []*proto.Task {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*proto.Task, len(b.tasks))
	copy(out, b.tasks)
	return out
}

type cancelRecordingBackend struct {
	recordingBackend
	canceled string
}

func (b *cancelRecordingBackend) CancelRun(_ context.Context, runID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.canceled = runID
	return nil
}

func (b *cancelRecordingBackend) canceledRun() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.canceled
}

func TestNextStrictLabelMatching(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "labels"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{
				Name:   "gpu-step",
				Run:    pipeline.Run{Command: []string{"echo", "gpu"}},
				Runner: pipeline.RunnerSelector{Label: "gpu"},
			},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, pl, dag, "run-labels", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if task := q.Next(""); task != nil {
		t.Fatalf("unlabeled worker got labeled task: %s", task.ID)
	}
	if task := q.Next("cpu"); task != nil {
		t.Fatalf("mismatched worker got labeled task: %s", task.ID)
	}
	task := q.Next("gpu")
	if task == nil {
		t.Fatal("matching worker did not get labeled task")
	}
	if task.StepName != "gpu-step" {
		t.Fatalf("got step %q, want gpu-step", task.StepName)
	}
}

func TestNextUnlabeledTaskMatchesAnyWorker(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "labels"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "any-step", Run: pipeline.Run{Command: []string{"echo", "any"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, pl, dag, "run-any", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.Next("gpu")
	if task == nil {
		t.Fatal("labeled worker did not get unlabeled task")
	}
	if task.StepName != "any-step" {
		t.Fatalf("got step %q, want any-step", task.StepName)
	}
}

func TestCompleteRetriesBeforeSkippingDownstream(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "retry"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"false"}}},
			{Name: "second", Run: pipeline.Run{Command: []string{"echo", "second"}}, DependsOn: []string{"first"}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetRetryPolicy(2, 0)
	q.Add(ctx, pl, dag, "run-retry", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	firstAttempt := q.Next("")
	if firstAttempt == nil || firstAttempt.Attempt != 1 {
		t.Fatalf("first attempt = %#v, want attempt 1", firstAttempt)
	}
	now := time.Now()
	if err := q.Complete(ctx, firstAttempt.ID, proto.TaskStatusFailed, "boom", now, now, 1); err != nil {
		t.Fatal(err)
	}
	if skipped := stepRepo.steps["run-retry:second"]; skipped != nil {
		t.Fatalf("downstream skipped before retry exhausted: %#v", skipped)
	}

	secondAttempt := q.Next("")
	if secondAttempt == nil {
		t.Fatal("retry attempt was not requeued")
	}
	if secondAttempt.ID != firstAttempt.ID {
		t.Fatalf("retry task ID = %q, want %q", secondAttempt.ID, firstAttempt.ID)
	}
	if secondAttempt.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", secondAttempt.Attempt)
	}
	if err := q.Complete(ctx, secondAttempt.ID, proto.TaskStatusFailed, "boom again", now, now, 1); err != nil {
		t.Fatal(err)
	}
	failed := stepRepo.steps["run-retry:first"]
	if failed == nil || failed.Status != proto.TaskStatusFailed || failed.Attempts != 2 {
		t.Fatalf("first step = %#v, want failed with 2 attempts", failed)
	}
	skipped := stepRepo.steps["run-retry:second"]
	if skipped == nil || skipped.Status != "skipped" {
		t.Fatalf("second step = %#v, want skipped", skipped)
	}
	if runRepo.status["run-retry"] != run.StatusFailed {
		t.Fatalf("run status = %q, want failed", runRepo.status["run-retry"])
	}
}

func TestCompleteCanSucceedAfterRetry(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "retry-success"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"sometimes"}}},
			{Name: "second", Run: pipeline.Run{Command: []string{"echo", "second"}}, DependsOn: []string{"first"}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetRetryPolicy(2, 0)
	q.Add(ctx, pl, dag, "run-retry-success", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	firstAttempt := q.Next("")
	if firstAttempt == nil {
		t.Fatal("first attempt was not dispatched")
	}
	now := time.Now()
	if err := q.Complete(ctx, firstAttempt.ID, proto.TaskStatusFailed, "temporary", now, now, 1); err != nil {
		t.Fatal(err)
	}
	secondAttempt := q.Next("")
	if secondAttempt == nil {
		t.Fatal("retry attempt was not dispatched")
	}
	if err := q.Complete(ctx, secondAttempt.ID, proto.TaskStatusDone, "", now, now, 1); err != nil {
		t.Fatal(err)
	}
	child := q.Next("")
	if child == nil || child.StepName != "second" {
		t.Fatalf("child task = %#v, want second ready after retry success", child)
	}
	if child.Attempt != 1 {
		t.Fatalf("child attempt = %d, want 1", child.Attempt)
	}
	if err := q.Complete(ctx, child.ID, proto.TaskStatusDone, "", now, now, 1); err != nil {
		t.Fatal(err)
	}
	if runRepo.status["run-retry-success"] != run.StatusSuccess {
		t.Fatalf("run status = %q, want success", runRepo.status["run-retry-success"])
	}
	first := stepRepo.steps["run-retry-success:first"]
	if first == nil || first.Status != proto.TaskStatusDone || first.Attempts != 2 {
		t.Fatalf("first step = %#v, want done with 2 attempts", first)
	}
}

func TestBackendRetryRedispatchesWithNextAttempt(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "backend-retry"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"false"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	backend := &recordingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRetryPolicy(2, 0)
	q.SetBackend(backend)
	q.Add(ctx, pl, dag, "run-backend-retry", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	var dispatched []*proto.Task
	if !waitUntil(2*time.Second, func() bool {
		dispatched = backend.snapshot()
		return len(dispatched) == 1
	}) {
		t.Fatalf("dispatches = %d, want 1", len(dispatched))
	}
	if dispatched[0].Attempt != 1 {
		t.Fatalf("first dispatch attempt = %d, want 1", dispatched[0].Attempt)
	}

	now := time.Now()
	if err := q.Complete(ctx, dispatched[0].ID, proto.TaskStatusFailed, "temporary", now, now, 1); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(2*time.Second, func() bool {
		dispatched = backend.snapshot()
		return len(dispatched) == 2
	}) {
		t.Fatalf("dispatches = %d, want retry dispatch", len(dispatched))
	}
	if dispatched[1].ID != dispatched[0].ID {
		t.Fatalf("retry ID = %q, want %q", dispatched[1].ID, dispatched[0].ID)
	}
	if dispatched[1].Attempt != 2 {
		t.Fatalf("retry dispatch attempt = %d, want 2", dispatched[1].Attempt)
	}
}

func TestCancelStopsPendingRetryTimer(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "retry-cancel"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"false"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	backend := &recordingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRetryPolicy(2, 50*time.Millisecond)
	q.SetBackend(backend)
	q.Add(ctx, pl, dag, "run-retry-cancel", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	var dispatched []*proto.Task
	if !waitUntil(2*time.Second, func() bool {
		dispatched = backend.snapshot()
		return len(dispatched) == 1
	}) {
		t.Fatalf("dispatches = %d, want 1", len(dispatched))
	}
	now := time.Now()
	if err := q.Complete(ctx, dispatched[0].ID, proto.TaskStatusFailed, "temporary", now, now, 1); err != nil {
		t.Fatal(err)
	}
	if err := q.Cancel(ctx, "run-retry-cancel"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(backend.snapshot()); got != 1 {
		t.Fatalf("dispatches after cancel = %d, want 1", got)
	}
}

func TestCleanupUsesRunningStartTimeNotQueueAddedAt(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "cleanup"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"sleep", "60"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	q := NewQueue(context.Background(), runRepo, &memoryStepRepo{})
	q.Add(ctx, pl, dag, "run-cleanup", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	q.runs["run-cleanup"].addedAt = time.Now().Add(-time.Hour)
	q.Cleanup(ctx, time.Second)
	if runRepo.status["run-cleanup"] == run.StatusFailed {
		t.Fatal("ready task expired before it started running")
	}
	if stats := q.Stats(); stats.Ready != 1 {
		t.Fatalf("stats after cleanup = %+v, want one ready task", stats)
	}

	task := q.Next("")
	if task == nil {
		t.Fatal("expected task to start")
	}
	q.runs["run-cleanup"].tasks["first"].startedAt = ptrTime(time.Now().Add(-time.Hour))
	q.Cleanup(ctx, time.Second)
	if runRepo.status["run-cleanup"] != run.StatusFailed {
		t.Fatalf("run status = %q, want failed", runRepo.status["run-cleanup"])
	}
}

func TestCancelRemovesQueuedRunAndMarksStepsCanceled(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "cancel"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"sleep", "60"}}},
			{Name: "second", Run: pipeline.Run{Command: []string{"echo", "second"}}, DependsOn: []string{"first"}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	backend := &cancelRecordingBackend{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetBackend(backend)
	q.Add(ctx, pl, dag, "run-cancel", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(backend.snapshot()) == 1
	}) {
		t.Fatal("first task was not dispatched")
	}
	if err := q.Cancel(ctx, "run-cancel"); err != nil {
		t.Fatal(err)
	}
	if got := backend.canceledRun(); got != "run-cancel" {
		t.Fatalf("backend canceled run = %q, want run-cancel", got)
	}
	if runRepo.status["run-cancel"] != run.StatusCanceled {
		t.Fatalf("run status = %q, want canceled", runRepo.status["run-cancel"])
	}
	for _, stepName := range []string{"first", "second"} {
		step := stepRepo.steps["run-cancel:"+stepName]
		if step == nil || step.Status != "canceled" {
			t.Fatalf("%s step = %#v, want canceled", stepName, step)
		}
	}
	if task := q.Next(""); task != nil {
		t.Fatalf("got task after cancel: %#v", task)
	}
	now := time.Now()
	if err := q.Complete(ctx, "run-cancel:first", proto.TaskStatusDone, "", now, now, 1); err != nil {
		t.Fatalf("late completion after cancel returned error: %v", err)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
