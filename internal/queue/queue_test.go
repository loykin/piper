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
func (r *memoryRunRepo) Get(context.Context, string) (*run.Run, error) {
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
	q := NewQueue(&memoryRunRepo{}, &memoryStepRepo{})
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
	q := NewQueue(&memoryRunRepo{}, &memoryStepRepo{})
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
	q := NewQueue(runRepo, stepRepo)
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
	q := NewQueue(runRepo, stepRepo)
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
	q := NewQueue(&memoryRunRepo{}, &memoryStepRepo{})
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
