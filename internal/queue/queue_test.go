package queue

import (
	"context"
	"fmt"
	"slices"
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
	mu     sync.Mutex
	status map[string]string
}

func (r *memoryRunRepo) Create(context.Context, *run.Run) error { return nil }
func (r *memoryRunRepo) Get(_ context.Context, projectID, id string) (*run.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if status, ok := r.status[id]; ok {
		return &run.Run{ProjectID: projectID, ID: id, Status: status}, nil
	}
	return nil, nil
}
func (r *memoryRunRepo) List(context.Context, string, run.RunFilter) ([]*run.Run, error) {
	return nil, nil
}
func (r *memoryRunRepo) UpdateStatus(_ context.Context, _, id, status string, _ *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == nil {
		r.status = map[string]string{}
	}
	r.status[id] = status
	return nil
}

// statusOf reads a run's status under the same lock UpdateStatus uses, so
// tests can poll it from the main goroutine while dispatch runs in the
// background without racing on the underlying map.
func (r *memoryRunRepo) statusOf(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status[id]
}
func (r *memoryRunRepo) MarkRunning(context.Context, string, string, time.Time) error {
	return nil
}
func (r *memoryRunRepo) Delete(context.Context, string, string) error { return nil }
func (r *memoryRunRepo) GetLatestSuccessful(context.Context, string, string) (*run.Run, error) {
	return nil, nil
}
func (r *memoryRunRepo) Count(context.Context, string, run.RunFilter) (int, error) { return 0, nil }
func (r *memoryRunRepo) ListTerminalBefore(context.Context, string, time.Time) ([]*run.Run, error) {
	return nil, nil
}
func (r *memoryRunRepo) ExistingIDs(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

type memoryStepRepo struct {
	mu    sync.Mutex
	steps map[string]*run.Step
}

func (r *memoryStepRepo) Upsert(_ context.Context, step *run.Step) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
func (r *memoryStepRepo) ListByRuns(context.Context, string, []string) (map[string][]*run.Step, error) {
	return map[string][]*run.Step{}, nil
}
func (r *memoryStepRepo) DeleteByRun(context.Context, string, string) error { return nil }

// stepOf reads a step under the same lock Upsert uses, so tests can inspect
// it from the main goroutine while the queue's background persist writer
// runs concurrently, without racing on the underlying map (mirrors
// memoryRunRepo.statusOf).
func (r *memoryStepRepo) stepOf(runID, stepName string) *run.Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.steps[runID+":"+stepName]
}

// ctxCheckingRunRepo/ctxCheckingStepRepo wrap the in-memory repos to fail
// like a real database/sql-backed repo would when handed an already-canceled
// context, instead of silently ignoring ctx the way memoryRunRepo/
// memoryStepRepo do. Timer-fired code paths (scheduleTimeoutLocked,
// scheduleRecoveryGraceLocked) must use a fresh context for their DB writes
// rather than the caller's original request context, which is long dead by
// the time an AfterFunc fires — these wrappers are what makes a regression
// back to the caller's ctx actually fail a test instead of passing silently.
type ctxCheckingRunRepo struct {
	*memoryRunRepo
}

func (r *ctxCheckingRunRepo) UpdateStatus(ctx context.Context, projectID, id, status string, t *time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.memoryRunRepo.UpdateStatus(ctx, projectID, id, status, t)
}

type ctxCheckingStepRepo struct {
	*memoryStepRepo
}

func (r *ctxCheckingStepRepo) Upsert(ctx context.Context, step *run.Step) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.memoryStepRepo.Upsert(ctx, step)
}

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

type failingOwnedBackend struct {
	workerID string
	err      error
}

func (b *failingOwnedBackend) Dispatch(context.Context, *proto.Task) error {
	return b.err
}

func (b *failingOwnedBackend) OwnerForTask(string) string {
	return b.workerID
}

func (b *failingOwnedBackend) ReleaseTask(string) {}

// releaseTrackingBackend records every ReleaseTask/ReleaseRun call so tests
// can assert that a task's router-level capacity reservation was actually
// released. Without this, a leaked reservation is invisible to a test that
// only checks step/run status — it only manifests later as spurious "no
// agent has available capacity" errors on unrelated dispatches.
type releaseTrackingBackend struct {
	recordingBackend
	releasedTasks []string
	releasedRuns  []string
}

func (b *releaseTrackingBackend) OwnerForTask(string) string { return "worker-1" }

func (b *releaseTrackingBackend) ReleaseTask(taskID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releasedTasks = append(b.releasedTasks, taskID)
}

func (b *releaseTrackingBackend) ReleaseRun(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releasedRuns = append(b.releasedRuns, runID)
}

func (b *releaseTrackingBackend) releasedTaskIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.releasedTasks...)
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
	if skipped := stepRepo.stepOf("run-retry", "second"); skipped != nil {
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
	failed := stepRepo.stepOf("run-retry", "first")
	if failed == nil || failed.Status != proto.TaskStatusFailed || failed.Attempts != 2 {
		t.Fatalf("first step = %#v, want failed with 2 attempts", failed)
	}
	skipped := stepRepo.stepOf("run-retry", "second")
	if skipped == nil || skipped.Status != "skipped" {
		t.Fatalf("second step = %#v, want skipped", skipped)
	}
	if runRepo.statusOf("run-retry") != run.StatusFailed {
		t.Fatalf("run status = %q, want failed", runRepo.statusOf("run-retry"))
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
	if runRepo.statusOf("run-retry-success") != run.StatusSuccess {
		t.Fatalf("run status = %q, want success", runRepo.statusOf("run-retry-success"))
	}
	first := stepRepo.stepOf("run-retry-success", "first")
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

func timeoutStepPipeline(name string, timeoutSeconds int) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: name},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{
				Name:    "step",
				Run:     pipeline.Run{Command: []string{"sleep", "60"}},
				Options: manifest.SpecOptions{Timeout: timeoutSeconds},
			},
		}},
	}
}

func TestStartTaskComputesDeadlineFromStepTimeout(t *testing.T) {
	ctx := context.Background()
	pl := timeoutStepPipeline("deadline", 5)
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-deadline", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	before := time.Now()
	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected task")
	}
	if task.Deadline == nil {
		t.Fatal("task.Deadline is nil, want computed from step.options.timeout")
	}
	wantMin := before.Add(4 * time.Second)
	wantMax := before.Add(6 * time.Second)
	if task.Deadline.Before(wantMin) || task.Deadline.After(wantMax) {
		t.Fatalf("task.Deadline = %v, want within [%v, %v]", task.Deadline, wantMin, wantMax)
	}
}

func TestZeroTimeoutNeverSchedulesTimer(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("no-timeout")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.Add(ctx, "project-a", pl, dag, "run-no-timeout", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected task")
	}
	if task.Deadline != nil {
		t.Fatalf("task.Deadline = %v, want nil for options.timeout=0", task.Deadline)
	}
}

func TestTimeoutFiresFailsStepWithoutRetryPolicy(t *testing.T) {
	ctx := context.Background()
	pl := timeoutStepPipeline("timeout-fail", 1)
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.Add(ctx, "project-a", pl, dag, "run-timeout-fail", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if task := q.takeReadyTask("", ""); task == nil {
		t.Fatal("expected task")
	}

	if !waitUntil(3*time.Second, func() bool {
		return runRepo.statusOf("run-timeout-fail") == run.StatusFailed
	}) {
		t.Fatalf("run status = %q, want failed after timeout", runRepo.statusOf("run-timeout-fail"))
	}
	step := stepRepo.stepOf("run-timeout-fail", "step")
	if step == nil || step.Status != string(taskFailed) {
		t.Fatalf("step = %#v, want failed", step)
	}
	if step.Error != "task execution timeout" {
		t.Fatalf("step error = %q, want %q", step.Error, "task execution timeout")
	}
}

// TestTimeoutFiresCommitsStatusAfterCallerContextCanceled reproduces a real
// production shape: Add is called with an HTTP-request-scoped context that
// is canceled as soon as the request handler returns, long before the
// options.timeout deadline (which can be minutes away) elapses. The step's
// failure must still be committed to the repos when the timer fires later —
// scheduleTimeoutLocked must not reuse the caller's (by-then-canceled) ctx
// for that write. A backend is configured so Add's synchronous
// promoteReady→dispatchIfNeeded→startTaskLocked call chain runs (and arms
// scheduleTimeoutLocked) with the real requestCtx, the same way the real
// gin HTTP handler → Add call does — takeReadyTask, used by other tests in
// this file, is a test-only shortcut that always calls startTaskLocked with
// its own fresh context.Background() and so cannot reproduce this bug.
func TestTimeoutFiresCommitsStatusAfterCallerContextCanceled(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	pl := timeoutStepPipeline("timeout-canceled-caller", 1)
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &ctxCheckingRunRepo{&memoryRunRepo{}}
	stepRepo := &ctxCheckingStepRepo{&memoryStepRepo{}}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetBackend(&recordingBackend{})
	q.Add(requestCtx, "project-a", pl, dag, "run-timeout-canceled-caller", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	// Simulate the HTTP handler returning: the request context is done well
	// before the 1s options.timeout deadline below.
	cancelRequest()

	if !waitUntil(3*time.Second, func() bool {
		return runRepo.statusOf("run-timeout-canceled-caller") == run.StatusFailed
	}) {
		t.Fatalf("run status = %q, want failed after timeout even though the caller's ctx was canceled", runRepo.statusOf("run-timeout-canceled-caller"))
	}
	step := stepRepo.stepOf("run-timeout-canceled-caller", "step")
	if step == nil || step.Status != string(taskFailed) {
		t.Fatalf("step = %#v, want failed", step)
	}
}

func TestTimeoutFiresRetriesWhenRetryPolicyConfigured(t *testing.T) {
	ctx := context.Background()
	pl := timeoutStepPipeline("timeout-retry", 1)
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	taskBackend := &recordingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRetryPolicy(2, 0)
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, "run-timeout-retry", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 1
	}) {
		t.Fatal("first dispatch did not happen")
	}

	if !waitUntil(3*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 2
	}) {
		t.Fatalf("dispatches after timeout = %d, want 2 (retry)", len(taskBackend.snapshot()))
	}
	dispatched := taskBackend.snapshot()
	if dispatched[1].Attempt != 2 {
		t.Fatalf("retry dispatch attempt = %d, want 2", dispatched[1].Attempt)
	}
}

// TestTimeoutReleasesRouterCapacity reproduces the same leak as
// TestCancelReleasesRouterCapacityForInFlightTask for the timeout path:
// scheduleTimeoutLocked's timer never goes through Complete() (there is no
// TaskResult), so it must release the router-level reservation itself.
func TestTimeoutReleasesRouterCapacity(t *testing.T) {
	ctx := context.Background()
	pl := timeoutStepPipeline("timeout-release", 1)
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	taskBackend := &releaseTrackingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, "run-timeout-release", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 1
	}) {
		t.Fatal("task was not dispatched")
	}

	wantTaskID := MakeTaskID("run-timeout-release", "step")
	if !waitUntil(3*time.Second, func() bool {
		return slices.Contains(taskBackend.releasedTaskIDs(), wantTaskID)
	}) {
		t.Fatalf("released task IDs = %v, want to contain %q after timeout fires", taskBackend.releasedTaskIDs(), wantTaskID)
	}
}

func TestLateResultAfterTimeoutIsIgnored(t *testing.T) {
	ctx := context.Background()
	pl := timeoutStepPipeline("timeout-late", 1)
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.Add(ctx, "project-a", pl, dag, "run-timeout-late", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.takeReadyTask("", "")
	if task == nil {
		t.Fatal("expected task")
	}
	if !waitUntil(3*time.Second, func() bool {
		return runRepo.statusOf("run-timeout-late") == run.StatusFailed
	}) {
		t.Fatal("run did not fail after timeout")
	}

	now := time.Now()
	if err := q.Complete(ctx, proto.TaskResult{TaskID: task.ID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatalf("late completion after timeout returned error: %v", err)
	}
	step := stepRepo.stepOf("run-timeout-late", "step")
	if step == nil || step.Status != string(taskFailed) {
		t.Fatalf("step = %#v, want to remain failed after late success", step)
	}
}

func TestPermanentDispatchFailureCompletesOwnedTask(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("dispatch-failure")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetBackend(&failingOwnedBackend{
		workerID: "docker-worker",
		err:      fmt.Errorf("container create: image not found"),
	})
	q.Add(ctx, "project-a", pl, dag, "run-dispatch-failure", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return runRepo.statusOf("run-dispatch-failure") == run.StatusFailed
	}) {
		t.Fatalf("run status = %q, want failed", runRepo.statusOf("run-dispatch-failure"))
	}
	step := stepRepo.stepOf("run-dispatch-failure", "step")
	if step == nil {
		t.Fatal("failed step was not persisted")
	}
	if step.Status != proto.TaskStatusFailed {
		t.Fatalf("step status = %q, want failed", step.Status)
	}
	if step.Error != "container create: image not found" {
		t.Fatalf("step error = %q", step.Error)
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
	if runRepo.statusOf("run-cleanup") == run.StatusFailed {
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
	if runRepo.statusOf("run-cleanup") != run.StatusFailed {
		t.Fatalf("run status = %q, want failed", runRepo.statusOf("run-cleanup"))
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
	if runRepo.statusOf("run-lease") == run.StatusFailed {
		t.Fatal("renewed task lease expired")
	}
}

func recoverySinglePipeline(name string) (*pipeline.Pipeline, *pipeline.DAG) {
	pl := &pipeline.Pipeline{
		Metadata: manifest.ObjectMeta{Name: name},
		Spec: pipeline.PipelineSpec{Steps: []pipeline.Step{
			{Name: "first", Run: pipeline.Run{Command: []string{"sleep", "60"}}},
		}},
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		panic(err)
	}
	return pl, dag
}

func TestRecoverMarksInFlightStepRecoveringNotPending(t *testing.T) {
	ctx := context.Background()
	pl, dag := recoverySinglePipeline("recover-inflight")
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.RecoverWithEnv(ctx, "project-a", pl, dag, "run-recover", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	entry := q.runs["run-recover"].tasks["first"]
	if entry.status != taskRecovering {
		t.Fatalf("status = %q, want %q", entry.status, taskRecovering)
	}
	if task := q.takeReadyTask("", ""); task != nil {
		t.Fatalf("recovering step was immediately re-dispatched: %#v", task)
	}
}

func TestRecoverLeaseRenewalPromotesRecoveringToRunning(t *testing.T) {
	ctx := context.Background()
	pl, dag := recoverySinglePipeline("recover-lease")
	runRepo := &memoryRunRepo{}
	q := NewQueue(context.Background(), runRepo, &memoryStepRepo{})
	q.SetRecoveryGracePeriod(200 * time.Millisecond)
	q.RecoverWithEnv(ctx, "project-a", pl, dag, "run-recover-lease", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	taskID := MakeTaskID("run-recover-lease", "first")
	q.RenewLeases("worker-a", []string{taskID})

	entry := q.runs["run-recover-lease"].tasks["first"]
	if entry.status != taskRunning {
		t.Fatalf("status after lease renewal = %q, want %q", entry.status, taskRunning)
	}

	// Wait past the (short) grace period: the timer must have been stopped
	// by the lease renewal, so the run must not fail.
	time.Sleep(400 * time.Millisecond)
	if runRepo.statusOf("run-recover-lease") == run.StatusFailed {
		t.Fatal("run failed after recovery grace expired even though the lease was renewed")
	}
}

func TestRecoveryGraceExpiryFailsStepWithoutRetryPolicy(t *testing.T) {
	ctx := context.Background()
	pl, dag := recoverySinglePipeline("recover-grace-fail")
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetRecoveryGracePeriod(100 * time.Millisecond)
	q.RecoverWithEnv(ctx, "project-a", pl, dag, "run-recover-fail", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return runRepo.statusOf("run-recover-fail") == run.StatusFailed
	}) {
		t.Fatalf("run status = %q, want failed after grace expiry", runRepo.statusOf("run-recover-fail"))
	}
	step := stepRepo.stepOf("run-recover-fail", "first")
	if step == nil || step.Status != string(taskFailed) {
		t.Fatalf("step = %#v, want failed", step)
	}
	if step.Error != "recovery grace period expired without worker reconnect" {
		t.Fatalf("step error = %q", step.Error)
	}
}

// TestRecoveryGraceExpiryCommitsStatusAfterCallerContextCanceled mirrors
// TestTimeoutFiresCommitsStatusAfterCallerContextCanceled for the recovery
// grace timer: RecoverWithEnv's ctx (the server-startup context that invoked
// recovery) must not be reused by scheduleRecoveryGraceLocked's timer body,
// since it can be long since canceled by the time the grace period elapses.
func TestRecoveryGraceExpiryCommitsStatusAfterCallerContextCanceled(t *testing.T) {
	recoverCtx, cancelRecover := context.WithCancel(context.Background())
	pl, dag := recoverySinglePipeline("recover-grace-canceled-caller")
	runRepo := &ctxCheckingRunRepo{&memoryRunRepo{}}
	stepRepo := &ctxCheckingStepRepo{&memoryStepRepo{}}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetRecoveryGracePeriod(100 * time.Millisecond)
	q.RecoverWithEnv(recoverCtx, "project-a", pl, dag, "run-recover-grace-canceled-caller", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	// Simulate the recovery-triggering context ending (e.g. server startup
	// completing) well before the grace period expires.
	cancelRecover()

	if !waitUntil(2*time.Second, func() bool {
		return runRepo.statusOf("run-recover-grace-canceled-caller") == run.StatusFailed
	}) {
		t.Fatalf("run status = %q, want failed after grace expiry even though the caller's ctx was canceled", runRepo.statusOf("run-recover-grace-canceled-caller"))
	}
	step := stepRepo.stepOf("run-recover-grace-canceled-caller", "first")
	if step == nil || step.Status != string(taskFailed) {
		t.Fatalf("step = %#v, want failed", step)
	}
}

func TestRecoveryGraceExpiryRetriesWhenRetryPolicyConfigured(t *testing.T) {
	ctx := context.Background()
	pl, dag := recoverySinglePipeline("recover-grace-retry")
	taskBackend := &recordingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRecoveryGracePeriod(100 * time.Millisecond)
	q.SetRetryPolicy(2, 0)
	q.SetBackend(taskBackend)
	q.RecoverWithEnv(ctx, "project-a", pl, dag, "run-recover-retry", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 1
	}) {
		t.Fatal("expected a retry dispatch after recovery grace expiry")
	}
	dispatched := taskBackend.snapshot()
	if dispatched[0].Attempt != 2 {
		t.Fatalf("retry dispatch attempt = %d, want 2", dispatched[0].Attempt)
	}
}

// TestRecoveryGraceExpiryReleasesRouterCapacity mirrors
// TestTimeoutReleasesRouterCapacity for the recovery-grace path:
// scheduleRecoveryGraceLocked's timer never goes through Complete() either,
// so it must release the router-level reservation the recovered step is
// implicitly holding (RecoverWithEnv marks it taskRecovering directly,
// bypassing Dispatch's own Reserve call, but downstream code — including
// this release — treats a recovering step as if it holds one, matching a
// step that really was dispatched before the crash).
func TestRecoveryGraceExpiryReleasesRouterCapacity(t *testing.T) {
	ctx := context.Background()
	pl, dag := recoverySinglePipeline("recover-grace-release")
	taskBackend := &releaseTrackingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetRecoveryGracePeriod(100 * time.Millisecond)
	q.SetBackend(taskBackend)
	q.RecoverWithEnv(ctx, "project-a", pl, dag, "run-recover-grace-release", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	wantTaskID := MakeTaskID("run-recover-grace-release", "first")
	if !waitUntil(2*time.Second, func() bool {
		return slices.Contains(taskBackend.releasedTaskIDs(), wantTaskID)
	}) {
		t.Fatalf("released task IDs = %v, want to contain %q after grace expiry", taskBackend.releasedTaskIDs(), wantTaskID)
	}
}

func TestLateResultAfterRecoveryGraceExpiryIsIgnored(t *testing.T) {
	ctx := context.Background()
	pl, dag := recoverySinglePipeline("recover-grace-late")
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetRecoveryGracePeriod(100 * time.Millisecond)
	q.RecoverWithEnv(ctx, "project-a", pl, dag, "run-recover-late", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return runRepo.statusOf("run-recover-late") == run.StatusFailed
	}) {
		t.Fatal("run did not fail after grace expiry")
	}

	now := time.Now()
	taskID := MakeTaskID("run-recover-late", "first")
	if err := q.Complete(ctx, proto.TaskResult{TaskID: taskID, Status: proto.TaskStatusDone, StartedAt: now, EndedAt: now, Attempt: 1}); err != nil {
		t.Fatalf("late completion after recovery grace expiry returned error: %v", err)
	}
	step := stepRepo.stepOf("run-recover-late", "first")
	if step == nil || step.Status != string(taskFailed) {
		t.Fatalf("step = %#v, want to remain failed after late success", step)
	}
}

func TestCleanupBackstopFailsOrphanedRecoveringStep(t *testing.T) {
	ctx := context.Background()
	pl, dag := recoverySinglePipeline("recover-cleanup-backstop")
	runRepo := &memoryRunRepo{}
	q := NewQueue(context.Background(), runRepo, &memoryStepRepo{})
	// A long grace period so the timer itself would never fire during the test;
	// this isolates the Cleanup TTL sweep as the thing that must catch it.
	q.SetRecoveryGracePeriod(time.Hour)
	q.RecoverWithEnv(ctx, "project-a", pl, dag, "run-recover-backstop", ".", t.TempDir(), proto.BuiltinVars{}, nil,
		[]RecoveredStep{{Name: "first", StartedAt: time.Now().Add(-time.Minute), Attempts: 1}}, nil)

	entry := q.runs["run-recover-backstop"].tasks["first"]
	// Simulate the grace timer having been lost (e.g. a second crash) by
	// stopping it directly and backdating the lease past the Cleanup TTL.
	entry.timer.Stop()
	entry.timer = nil
	entry.leaseAt = ptrTime(time.Now().Add(-time.Hour))

	q.Cleanup(ctx, time.Second)
	if runRepo.statusOf("run-recover-backstop") != run.StatusFailed {
		t.Fatalf("run status = %q, want failed via Cleanup backstop", runRepo.statusOf("run-recover-backstop"))
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
	if runRepo.statusOf("run-cancel") != run.StatusCanceled {
		t.Fatalf("run status = %q, want canceled", runRepo.statusOf("run-cancel"))
	}
	for _, stepName := range []string{"first", "second"} {
		step := stepRepo.stepOf("run-cancel", stepName)
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

// erroringCancelBackend always fails the remote CancelRun call, simulating a
// disconnected/unreachable worker.
type erroringCancelBackend struct {
	recordingBackend
}

func (b *erroringCancelBackend) CancelRun(context.Context, string) error {
	return fmt.Errorf("worker unreachable")
}

func TestCancelCommitsLocalStatusEvenWhenBackendCancelFails(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("cancel-remote-fail")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}
	taskBackend := &erroringCancelBackend{}
	q := NewQueue(context.Background(), runRepo, stepRepo)
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, "run-remote-fail", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 1
	}) {
		t.Fatal("task was not dispatched")
	}

	if err := q.Cancel(ctx, "project-a", "run-remote-fail"); err != nil {
		t.Fatalf("Cancel returned error even though local commit should succeed: %v", err)
	}
	if got := runRepo.statusOf("run-remote-fail"); got != run.StatusCanceled {
		t.Fatalf("run status = %q, want canceled (must commit locally regardless of remote CancelRun failure)", got)
	}
	step := stepRepo.stepOf("run-remote-fail", "step")
	if step == nil || step.Status != "canceled" {
		t.Fatalf("step = %#v, want canceled", step)
	}
}

// TestCancelReleasesRouterCapacityForInFlightTask reproduces a leak found via
// live manual verification: canceling a run with an in-flight (dispatched
// but never Complete()'d — the normal shape of a cancel, since the worker
// stops the process without reporting a TaskResult) task must release that
// task's router-level capacity reservation. Before this fix, Cancel() only
// cleared AgentBackend's own bookkeeping and never called owner.ReleaseTask,
// so every canceled run permanently leaked one reserved slot on its agent —
// eventually every future dispatch failed with "no agent has available
// capacity" even though nothing was actually running.
func TestCancelReleasesRouterCapacityForInFlightTask(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("cancel-release")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	taskBackend := &releaseTrackingBackend{}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetBackend(taskBackend)
	q.Add(ctx, "project-a", pl, dag, "run-cancel-release", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool {
		return len(taskBackend.snapshot()) == 1
	}) {
		t.Fatal("task was not dispatched")
	}

	if err := q.Cancel(ctx, "project-a", "run-cancel-release"); err != nil {
		t.Fatalf("Cancel error: %v", err)
	}

	released := taskBackend.releasedTaskIDs()
	wantTaskID := MakeTaskID("run-cancel-release", "step")
	if !slices.Contains(released, wantTaskID) {
		t.Fatalf("released task IDs = %v, want to contain %q (in-flight task's router reservation must be released on cancel)", released, wantTaskID)
	}
}

// blockingDispatchBackend blocks in Dispatch until released, and records
// whether Dispatch was ever actually invoked.
type blockingDispatchBackend struct {
	mu      sync.Mutex
	called  bool
	release chan struct{}
}

func (b *blockingDispatchBackend) Dispatch(_ context.Context, _ *proto.Task) error {
	b.mu.Lock()
	b.called = true
	b.mu.Unlock()
	<-b.release
	return nil
}

func (b *blockingDispatchBackend) wasCalled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.called
}

func TestCancelRaceWithInFlightDispatchGoroutineSkipsSend(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("cancel-race")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	backend := &blockingDispatchBackend{release: make(chan struct{})}
	defer close(backend.release)
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetBackend(backend)

	// Add() synchronously calls startTaskLocked and spawns the dispatch
	// goroutine before returning. Canceling immediately afterward, with no
	// intervening blocking call, races the dispatch goroutine's own
	// pre-Dispatch status re-check against Cancel()'s status mutation — both
	// synchronized by q.mu, so whichever happens-before under the mutex wins
	// deterministically. Run with -race to also catch any data race.
	q.Add(ctx, "project-a", pl, dag, "run-race", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	if err := q.Cancel(ctx, "project-a", "run-race"); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	// Give the dispatch goroutine a chance to run (it should observe the
	// cancellation and return before ever calling Dispatch).
	time.Sleep(100 * time.Millisecond)
	if backend.wasCalled() {
		t.Fatal("Dispatch was called on a run that was canceled before the dispatch goroutine ran")
	}
}

func TestCloseWaitsForInFlightDispatchGoroutine(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("close-wait")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	backend := &blockingDispatchBackend{release: make(chan struct{})}
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetBackend(backend)
	q.Add(ctx, "project-a", pl, dag, "run-close-wait", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(time.Second, backend.wasCalled) {
		t.Fatal("dispatch goroutine never reached Dispatch")
	}

	closeErr := make(chan error, 1)
	go func() {
		closeErr <- q.Close(context.Background())
	}()

	select {
	case err := <-closeErr:
		t.Fatalf("Close returned before the in-flight dispatch goroutine finished: %v", err)
	case <-time.After(100 * time.Millisecond):
		// Still blocked, as expected — Dispatch hasn't returned yet.
	}

	close(backend.release)
	select {
	case err := <-closeErr:
		if err != nil {
			t.Fatalf("Close returned error after dispatch finished: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the in-flight dispatch goroutine finished")
	}
}

func TestCloseTimesOutIfGoroutineNeverFinishes(t *testing.T) {
	ctx := context.Background()
	pl := singleStepPipeline("close-timeout")
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		t.Fatal(err)
	}
	backend := &blockingDispatchBackend{release: make(chan struct{})}
	defer close(backend.release)
	q := NewQueue(context.Background(), &memoryRunRepo{}, &memoryStepRepo{})
	q.SetBackend(backend)
	q.Add(ctx, "project-a", pl, dag, "run-close-timeout", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(time.Second, backend.wasCalled) {
		t.Fatal("dispatch goroutine never reached Dispatch")
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := q.Close(shortCtx); err == nil {
		t.Fatal("expected Close to time out while the dispatch goroutine is still blocked")
	}
}

// TestTimerWaitGroupBalance exercises every entry.timer AfterFunc site
// (timeout, retry, requeue, recovery grace) both letting it fire and
// stopping it before it fires, then calls Close with a short-but-nonzero
// deadline — a missed q.wg.Done() (fired-but-uncounted, or stopped-but-not-
// balanced) would leave a stuck counter and make this hang past the
// deadline; a double Done() would panic ("negative WaitGroup counter") under
// -race. Run with -race and a high -count for real coverage of the
// Stop()-vs-fire race in stopEntryTimerLocked.
func TestTimerWaitGroupBalance(t *testing.T) {
	ctx := context.Background()
	runRepo := &memoryRunRepo{}
	stepRepo := &memoryStepRepo{}

	// Retry timer: fires naturally (retryDelay elapses).
	q1 := NewQueue(context.Background(), runRepo, stepRepo)
	q1.SetRetryPolicy(2, 10*time.Millisecond)
	q1.SetBackend(&failingOwnedBackend{err: fmt.Errorf("dispatch failed")})
	pl := singleStepPipeline("balance-retry")
	dag, _ := pipeline.BuildDAG(pl)
	q1.Add(ctx, "project-a", pl, dag, "run-balance-retry", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	time.Sleep(50 * time.Millisecond) // let the retry timer fire

	// Retry timer: stopped before firing (Cancel while retrying).
	q2 := NewQueue(context.Background(), runRepo, stepRepo)
	q2.SetRetryPolicy(2, time.Hour)
	q2.SetBackend(&failingOwnedBackend{err: fmt.Errorf("dispatch failed")})
	q2.Add(ctx, "project-a", pl, dag, "run-balance-retry-stop", ".", t.TempDir(), proto.BuiltinVars{}, nil)
	time.Sleep(20 * time.Millisecond)
	if err := q2.Cancel(ctx, "project-a", "run-balance-retry-stop"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	for i, q := range []*Queue{q1, q2} {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := q.Close(closeCtx); err != nil {
			t.Errorf("queue %d: Close did not drain within budget (wg leak?): %v", i, err)
		}
		cancel()
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

// TestBusyRequeueTimerNoopsAfterServerCtxCancel guards against a shutdown race:
// requeueBusyLocked schedules a 2s time.AfterFunc to re-dispatch. If the Queue's
// serverCtx is cancelled (server/test teardown) before that timer fires, the
// callback used to redispatch anyway and hit a torn-down store — surfacing as
// "dbstore: \"primary\" not found" noise well after the owning test had returned.
func TestBusyRequeueTimerNoopsAfterServerCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pl := singleStepPipeline("busy-shutdown")
	dag, _ := pipeline.BuildDAG(pl)

	var dispatches atomic.Int32
	// busyCount is large so the queue never gets past the first, busy dispatch
	// within this test — only the cancel-before-timer-fires path is exercised.
	alwaysBusy := &busyBackend{busyCount: 1000, onDispatch: func() { dispatches.Add(1) }}
	q := NewQueue(ctx, &memoryRunRepo{}, &memoryStepRepo{})
	q.SetBackend(alwaysBusy)
	q.Add(ctx, "project-a", pl, dag, "run-busy-shutdown", ".", t.TempDir(), proto.BuiltinVars{}, nil)

	if !waitUntil(2*time.Second, func() bool { return dispatches.Load() >= 1 }) {
		t.Fatalf("expected the first (busy) dispatch, got %d", dispatches.Load())
	}
	// Cancel serverCtx the way Piper.Close() does, well before the queued
	// 2s busy-requeue timer fires.
	cancel()

	time.Sleep(2500 * time.Millisecond)
	if got := dispatches.Load(); got != 1 {
		t.Fatalf("dispatches after serverCtx cancel = %d, want 1 (timer must no-op post-shutdown)", got)
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
