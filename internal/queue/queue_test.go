package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piper/piper/internal/pipelinedispatch"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/run"
)

func (q *Queue) takeReadyTask(workerID, label string) *proto.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, r := range q.runs {
		for _, entry := range r.tasks {
			if entry.status != taskReady {
				continue
			}
			if entry.task.WorkerID != "" && entry.task.WorkerID != workerID {
				continue
			}
			if entry.task.Label != "" && entry.task.Label != label {
				continue
			}
			entry.assignedWorkerID = workerID
			q.startTaskLocked(context.Background(), r.runID, entry)
			task := *entry.task
			task.WorkerID = workerID
			return &task
		}
	}
	return nil
}

type memoryRunRepo struct {
	status map[string]string
}

func (r *memoryRunRepo) Create(context.Context, *run.Run) error { return nil }
func (r *memoryRunRepo) Get(_ context.Context, projectID, id string) (*run.Run, error) {
	if status, ok := r.status[id]; ok {
		return &run.Run{ProjectID: projectID, ID: id, Status: status}, nil
	}
	return nil, nil
}
func (r *memoryRunRepo) List(context.Context, string, run.RunFilter) ([]*run.Run, error) {
	return nil, nil
}
func (r *memoryRunRepo) UpdateStatus(_ context.Context, _, id, status string, _ *time.Time) error {
	if r.status == nil {
		r.status = map[string]string{}
	}
	r.status[id] = status
	return nil
}
func (r *memoryRunRepo) MarkRunning(context.Context, string, string, time.Time) error {
	return nil
}
func (r *memoryRunRepo) Delete(context.Context, string, string) error { return nil }
func (r *memoryRunRepo) GetLatestSuccessful(context.Context, string, string) (*run.Run, error) {
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
func (r *memoryStepRepo) List(context.Context, string, string) ([]*run.Step, error) {
	return nil, nil
}
func (r *memoryStepRepo) DeleteByRun(context.Context, string, string) error { return nil }

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
		Metadata: manifest.ObjectMeta{Name: "labels"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{
				Name:   "gpu-step",
				Run:    pipeline.Run{Command: []string{"echo", "gpu"}},
				Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Label: "gpu"}},
			},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-labels", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if task := q.takeReadyTask("", ""); task != nil {
		t.Fatalf("unlabeled worker got labeled task: %s", task.ID)
	}
	if task := q.takeReadyTask("", "cpu"); task != nil {
		t.Fatalf("mismatched worker got labeled task: %s", task.ID)
	}
	task := q.takeReadyTask("", "gpu")
	if task == nil {
		t.Fatal("matching worker did not get labeled task")
	}
	if task.StepName != "gpu-step" {
		t.Fatalf("got step %q, want gpu-step", task.StepName)
	}
}

func TestReadyTaskHonorsPipelinePlacementWorker(t *testing.T) {
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "p"},
		Spec: pipeline.PipelineSpec{
			Defaults: &pipeline.PipelineDefaults{
				Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Worker: "worker-a"}},
			},
			Steps: []pipeline.Step{{
				Name: "s1",
				Run:  pipeline.Run{Command: []string{"echo", "ok"}},
			}},
		},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q.Add(context.Background(), "project-a", pl, dag, "run-1", "", "", proto.BuiltinVars{}, nil)

	if task := q.takeReadyTask("worker-b", ""); task != nil {
		t.Fatalf("worker-b got task %s, want nil", task.ID)
	}
	task := q.takeReadyTask("worker-a", "")
	if task == nil {
		t.Fatal("worker-a got nil task")
	}
	if task.WorkerID != "worker-a" {
		t.Fatalf("worker id = %q, want worker-a", task.WorkerID)
	}
	if task.ProjectID != "project-a" {
		t.Fatalf("project id = %q, want project-a", task.ProjectID)
	}
}

func TestNextUnlabeledTaskMatchesAnyWorker(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "labels"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "any-step", Run: pipeline.Run{Command: []string{"echo", "any"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-any", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.takeReadyTask("", "gpu")
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
		Metadata: manifest.ObjectMeta{Name: "retry"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
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
	q.Add(ctx, "project-a", pl, dag, "run-retry", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	firstAttempt := q.takeReadyTask("", "")
	if firstAttempt == nil || firstAttempt.Attempt != 1 {
		t.Fatalf("first attempt = %#v, want attempt 1", firstAttempt)
	}
	now := time.Now()
	if err := q.Complete(ctx, proto.TaskResult{TaskID: firstAttempt.ID, Status: proto.TaskStatusFailed, Error: "boom", StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if skipped := stepRepo.steps["run-retry:second"]; skipped != nil {
		t.Fatalf("downstream skipped before retry exhausted: %#v", skipped)
	}

	secondAttempt := q.takeReadyTask("", "")
	if secondAttempt == nil {
		t.Fatal("retry attempt was not requeued")
	}
	if secondAttempt.ID != firstAttempt.ID {
		t.Fatalf("retry task ID = %q, want %q", secondAttempt.ID, firstAttempt.ID)
	}
	if secondAttempt.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", secondAttempt.Attempt)
	}
	if err := q.Complete(ctx, proto.TaskResult{TaskID: secondAttempt.ID, Status: proto.TaskStatusFailed, Error: "boom again", StartedAt: now, EndedAt: now, Attempt: 2}); err != nil {
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
		Metadata: manifest.ObjectMeta{Name: "retry-success"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
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
	q.Add(ctx, "project-a", pl, dag, "run-retry-success", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	firstAttempt := q.takeReadyTask("", "")
	if firstAttempt == nil {
		t.Fatal("first attempt was not dispatched")
	}
	now := time.Now()
	if err := q.Complete(ctx, proto.TaskResult{TaskID: firstAttempt.ID, Status: proto.TaskStatusFailed, Error: "temporary", StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	secondAttempt := q.takeReadyTask("", "")
	if secondAttempt == nil {
		t.Fatal("retry attempt was not dispatched")
	}
	if err := q.Complete(ctx, proto.TaskResult{TaskID: secondAttempt.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 2}); err != nil {
		t.Fatal(err)
	}
	child := q.takeReadyTask("", "")
	if child == nil || child.StepName != "second" {
		t.Fatalf("child task = %#v, want second ready after retry success", child)
	}
	if child.Attempt != 1 {
		t.Fatalf("child attempt = %d, want 1", child.Attempt)
	}
	if err := q.Complete(ctx, proto.TaskResult{TaskID: child.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
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
		Metadata: manifest.ObjectMeta{Name: "backend-retry"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"false"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	taskBackend := &recordingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRetryPolicy(2, 0)
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, "run-backend-retry", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	var dispatched []*proto.Task
	if !waitUntil(2*time.Second, func() bool {
		dispatched = taskBackend.snapshot()
		return len(dispatched) == 1
	}) {
		t.Fatalf("dispatches = %d, want 1", len(dispatched))
	}
	if dispatched[0].Attempt != 1 {
		t.Fatalf("first dispatch attempt = %d, want 1", dispatched[0].Attempt)
	}

	now := time.Now()
	if err := q.Complete(ctx, proto.TaskResult{TaskID: dispatched[0].ID, Status: proto.TaskStatusFailed, Error: "temporary", StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(2*time.Second, func() bool {
		dispatched = taskBackend.snapshot()
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
		Metadata: manifest.ObjectMeta{Name: "retry-cancel"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"false"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	taskBackend := &recordingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRetryPolicy(2, 50*time.Millisecond)
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, "run-retry-cancel", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	var dispatched []*proto.Task
	if !waitUntil(2*time.Second, func() bool {
		dispatched = taskBackend.snapshot()
		return len(dispatched) == 1
	}) {
		t.Fatalf("dispatches = %d, want 1", len(dispatched))
	}
	now := time.Now()
	if err := q.Complete(ctx, proto.TaskResult{TaskID: dispatched[0].ID, Status: proto.TaskStatusFailed, Error: "temporary", StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := q.Cancel(ctx, "project-a", "run-retry-cancel"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(taskBackend.snapshot()); got != 1 {
		t.Fatalf("dispatches after cancel = %d, want 1", got)
	}
}

func TestCleanupUsesRunningStartTimeNotQueueAddedAt(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "cleanup"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"sleep", "60"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	q := NewQueue(context.Background(), runRepo, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-cleanup", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	q.runs["run-cleanup"].addedAt = time.Now().Add(-time.Hour)
	q.Cleanup(ctx, time.Second)
	if runRepo.status["run-cleanup"] == run.StatusFailed {
		t.Fatal("ready task expired before it started running")
	}
	if stats := q.Stats(); stats.Ready != 1 {
		t.Fatalf("stats after cleanup = %+v, want one ready task", stats)
	}

	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected task to start")
	}
	q.runs["run-cleanup"].tasks["first"].leaseAt = ptrTime(time.Now().Add(-time.Hour))
	q.Cleanup(ctx, time.Second)
	if runRepo.status["run-cleanup"] != run.StatusFailed {
		t.Fatalf("run status = %q, want failed", runRepo.status["run-cleanup"])
	}
}

func TestRenewLeasesPreventsRunningTaskExpiry(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "lease"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"sleep", "60"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	q := NewQueue(context.Background(), runRepo, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-lease", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	task := q.takeReadyTask("worker-a", "")
	if task == nil {
		t.Fatal("expected task")
	}
	q.runs["run-lease"].tasks["first"].leaseAt = ptrTime(time.Now().Add(-time.Hour))
	q.RenewLeases("worker-a", []string{task.ID})
	q.Cleanup(ctx, time.Second)
	if runRepo.status["run-lease"] == run.StatusFailed {
		t.Fatal("renewed task lease expired")
	}
}

func TestCompleteRejectsDifferentWorker(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "owner"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"true"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-owner", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	task := q.takeReadyTask("worker-a", "")
	if task == nil {
		t.Fatal("expected task")
	}
	now := time.Now()
	err = q.Complete(ctx, proto.TaskResult{
		TaskID: task.ID, WorkerID: "worker-b", Status: proto.TaskStatusDone,
		StartedAt: now, EndedAt: now, Attempt: 1,
	})
	if err == nil {
		t.Fatal("completion from non-owner was accepted")
	}
	if stats := q.Stats(); stats.Running != 1 {
		t.Fatalf("stats = %+v, want task still running", stats)
	}
}

func TestCancelRemovesQueuedRunAndMarksStepsCanceled(t *testing.T) {
	ctx := context.Background()
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: "cancel"},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
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
	taskBackend := &cancelRecordingBackend{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, "run-cancel", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 1
	}) {
		t.Fatal("first task was not dispatched")
	}
	if err := q.Cancel(ctx, "project-a", "run-cancel"); err != nil {
		t.Fatal(err)
	}
	if got := taskBackend.canceledRun(); got != "run-cancel" {
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
	if task := q.takeReadyTask("", ""); task != nil {
		t.Fatalf("got task after cancel: %#v", task)
	}
	now := time.Now()
	if err := q.Complete(ctx, proto.TaskResult{TaskID: "run-cancel:first", Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatalf("late completion after cancel returned error: %v", err)
	}
}

func TestCompleteDuplicateResultIsIgnored(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("dup")
	dag, _ := pipeline.BuildDAG(pl)
	q := NewQueue(ctx, &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-dup", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected task")
	}
	now := time.Now()
	result := proto.TaskResult{TaskID: task.ID, WorkerID: "", Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}
	if err := q.Complete(ctx, result); err != nil {
		t.Fatalf("first complete: %v", err)
	}
	// second identical result must be silently accepted (idempotent)
	if err := q.Complete(ctx, result); err != nil {
		t.Fatalf("duplicate complete returned error: %v", err)
	}
}

func TestCompleteStaleAttemptIsIgnored(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("stale")
	dag, _ := pipeline.BuildDAG(pl)
	q := NewQueue(ctx, &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRetryPolicy(2, 0)
	q.Add(ctx, "project-a", pl, dag, "run-stale", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected task")
	}
	now := time.Now()
	// fail attempt 1 → queue retries
	if err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusFailed, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatalf("fail attempt 1: %v", err)
	}
	// attempt 2 is now in progress
	task2 := q.takeReadyTask("", "")
	if task2 == nil {
		t.Fatal("no retry task available")
	}
	// stale result from attempt 1 arriving after attempt 2 started — must be ignored
	if err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatalf("stale attempt complete returned error: %v", err)
	}
	if s := q.Stats(); s.Running != 1 {
		t.Fatalf("stats = %+v, want task still running after stale result", s)
	}
}

func TestCompleteFutureAttemptIsRejected(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("future")
	dag, _ := pipeline.BuildDAG(pl)
	q := NewQueue(ctx, &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-future", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected task")
	}
	now := time.Now()
	err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 99})
	if err == nil {
		t.Fatal("future attempt was accepted, expected error")
	}
}

func TestDispatchRetryableErrorRequeuesWithoutConsumingAttempt(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("busy")
	dag, _ := pipeline.BuildDAG(pl)

	var dispatches atomic.Int32
	busyOnce := &busyBackend{busyCount: 1, onDispatch: func() { dispatches.Add(1) }}
	q := NewQueue(ctx, &memoryRunRepo{}, &memoryStepRepo{})
	q.SetBackend(busyOnce)
	q.Add(ctx, "project-a", pl, dag, "run-busy", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	// wait for both dispatch attempts: first busy, second success
	if !waitUntil(5*time.Second, func() bool { return dispatches.Load() >= 2 }) {
		t.Fatalf("expected 2 dispatch attempts, got %d", dispatches.Load())
	}
	// task must still be running (attempt count = 1, not 2)
	if s := q.Stats(); s.Running != 1 {
		t.Fatalf("stats = %+v, want 1 running task", s)
	}
	// the second dispatched task must have Attempt == 1 (not 2)
	busyOnce.mu.Lock()
	lastAttempt := busyOnce.lastAttempt
	busyOnce.mu.Unlock()
	if lastAttempt != 1 {
		t.Fatalf("last dispatch attempt = %d, want 1 (busy should not consume attempt)", lastAttempt)
	}
}

// busyBackend returns a retryable DispatchError for the first busyCount dispatches.
type busyBackend struct {
	mu          sync.Mutex
	busyCount   int
	dispatched  int
	lastAttempt int
	onDispatch  func()
}

func (b *busyBackend) Dispatch(_ context.Context, task *proto.Task) error {
	b.mu.Lock()
	b.dispatched++
	b.lastAttempt = task.Attempt
	busy := b.dispatched <= b.busyCount
	b.mu.Unlock()
	if b.onDispatch != nil {
		b.onDispatch()
	}
	if busy {
		return &pipelinedispatch.DispatchError{Retryable: true, Err: fmt.Errorf("worker busy")}
	}
	return nil
}

func singleStepPipeline(name string) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: name},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "step", Run: pipeline.Run{Command: []string{"echo", name}}},
		}},
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
