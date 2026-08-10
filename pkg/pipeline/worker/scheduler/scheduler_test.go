package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline/run"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

// fakeDriver is a minimal in-memory driver.Driver test double. Start/Wait
// are keyed by step name (not attempt), so a test can target "the current
// attempt" of a step without tracking attempt numbers itself — a Start call
// always installs a fresh completion channel for that step name.
type fakeDriver struct {
	mu       sync.Mutex
	starts   map[string]*proto.Task
	order    []string
	waiters  map[string]chan pdriver.Exit
	stops    map[string]int
	startErr map[string]error
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{
		starts:  make(map[string]*proto.Task),
		waiters: make(map[string]chan pdriver.Exit),
		stops:   make(map[string]int),
	}
}

func (d *fakeDriver) Start(_ context.Context, task *proto.Task, _ pdriver.ExecSpec) (pdriver.Handle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.startErr[task.StepName]; err != nil {
		return pdriver.Handle{}, err
	}
	d.starts[task.StepName] = task
	d.order = append(d.order, task.StepName)
	d.waiters[task.StepName] = make(chan pdriver.Exit, 1)
	return pdriver.Handle{
		RuntimeKey: task.StepName, TaskID: task.ID, RunID: task.RunID,
		StepName: task.StepName, Attempt: task.Attempt,
	}, nil
}

func (d *fakeDriver) Wait(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error) {
	d.mu.Lock()
	ch := d.waiters[handle.StepName]
	d.mu.Unlock()
	select {
	case exit := <-ch:
		return exit, nil
	case <-ctx.Done():
		return pdriver.Exit{}, ctx.Err()
	}
}

func (d *fakeDriver) Stop(_ context.Context, handle pdriver.Handle, _ time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stops[handle.StepName]++
	return nil
}

func (d *fakeDriver) Recover(context.Context) ([]pdriver.Handle, error) { return nil, nil }

// complete signals stepName's current (most recent Start's) Wait call to
// return exit successfully.
func (d *fakeDriver) complete(stepName string, exit pdriver.Exit) {
	d.mu.Lock()
	ch := d.waiters[stepName]
	d.mu.Unlock()
	ch <- exit
}

func (d *fakeDriver) startCount(stepName string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, s := range d.order {
		if s == stepName {
			n++
		}
	}
	return n
}

type finalizeCall struct {
	status  string
	endedAt time.Time
}

type fakeReporter struct {
	mu       sync.Mutex
	steps    []run.Step
	finalize []finalizeCall
}

func (r *fakeReporter) UpsertStep(s *run.Step) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, *s)
	return nil
}

func (r *fakeReporter) FinalizeRun(status string, endedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalize = append(r.finalize, finalizeCall{status: status, endedAt: endedAt})
	return nil
}

func (r *fakeReporter) finalizeCalls() []finalizeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]finalizeCall(nil), r.finalize...)
}

func (r *fakeReporter) stepsFor(name string) []run.Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []run.Step
	for _, s := range r.steps {
		if s.StepName == name {
			out = append(out, s)
		}
	}
	return out
}

func noopExecSpec(*proto.Task) (pdriver.ExecSpec, error) { return pdriver.ExecSpec{}, nil }

const twoStepYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: test-pipeline
spec:
  steps:
    - name: a
      run:
        command: ["echo", "a"]
    - name: b
      depends_on: [a]
      run:
        command: ["echo", "b"]
`

const oneStepYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: test-pipeline
spec:
  steps:
    - name: a
      run:
        command: ["echo", "a"]
`

const oneStepTimeoutYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: test-pipeline
spec:
  steps:
    - name: a
      options:
        timeout: 1
      run:
        command: ["echo", "a"]
`

func newScheduler(t *testing.T, yaml string, driver *fakeDriver, reporter *fakeReporter, maxAttempts int, retryDelay time.Duration) *RunScheduler {
	t.Helper()
	rs, err := New(proto.RunDispatch{
		ProjectID:    "proj-1",
		RunID:        "run-1",
		PipelineYAML: yaml,
	}, Options{
		Driver:        driver,
		Report:        reporter,
		BuildExecSpec: noopExecSpec,
		MaxAttempts:   maxAttempts,
		RetryDelay:    retryDelay,
		WorkerID:      "worker-1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rs
}

func waitCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met within timeout")
}

func TestRunSchedulerCallsAfterStartOnSuccessfulStart(t *testing.T) {
	driver := newFakeDriver()
	reporter := &fakeReporter{}

	var mu sync.Mutex
	var calls []string
	rs, err := New(proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: oneStepYAML}, Options{
		Driver:        driver,
		Report:        reporter,
		BuildExecSpec: noopExecSpec,
		AfterStart: func(_ context.Context, task *proto.Task, _ pdriver.ExecSpec, handle pdriver.Handle) {
			mu.Lock()
			calls = append(calls, task.StepName+":"+handle.RuntimeKey)
			mu.Unlock()
		},
		MaxAttempts: 1,
		WorkerID:    "worker-1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rs.Start()

	waitCond(t, func() bool { return driver.startCount("a") == 1 })
	driver.complete("a", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusDone}})
	waitCond(t, func() bool { return len(reporter.finalizeCalls()) == 1 })

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] != "a:a" {
		t.Fatalf("AfterStart calls = %#v, want exactly one call for step a", calls)
	}
}

func TestRunSchedulerPromotesReadyStepsInDependencyOrder(t *testing.T) {
	driver := newFakeDriver()
	reporter := &fakeReporter{}
	rs := newScheduler(t, twoStepYAML, driver, reporter, 1, 0)
	rs.Start()

	waitCond(t, func() bool { return driver.startCount("a") == 1 })
	if driver.startCount("b") != 0 {
		t.Fatal("step b started before its dependency a completed")
	}

	driver.complete("a", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusDone}})

	waitCond(t, func() bool { return driver.startCount("b") == 1 })
	driver.complete("b", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusDone}})

	waitCond(t, func() bool { return len(reporter.finalizeCalls()) == 1 })
	calls := reporter.finalizeCalls()
	if calls[0].status != run.StatusSuccess {
		t.Fatalf("finalize status = %q, want %q", calls[0].status, run.StatusSuccess)
	}
}

func TestRunSchedulerRetriesUntilMaxAttempts(t *testing.T) {
	driver := newFakeDriver()
	reporter := &fakeReporter{}
	rs := newScheduler(t, oneStepYAML, driver, reporter, 3, 0)
	rs.Start()

	waitCond(t, func() bool { return driver.startCount("a") == 1 })
	driver.complete("a", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusFailed, Error: "boom"}})

	waitCond(t, func() bool { return driver.startCount("a") == 2 })
	driver.complete("a", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusFailed, Error: "boom again"}})

	waitCond(t, func() bool { return driver.startCount("a") == 3 })
	driver.complete("a", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusDone}})

	waitCond(t, func() bool { return len(reporter.finalizeCalls()) == 1 })
	if got := reporter.finalizeCalls()[0].status; got != run.StatusSuccess {
		t.Fatalf("finalize status = %q, want %q (should succeed on 3rd attempt)", got, run.StatusSuccess)
	}
	steps := reporter.stepsFor("a")
	if len(steps) == 0 || steps[len(steps)-1].Attempts != 3 {
		t.Fatalf("final reported attempts = %#v, want 3", steps)
	}
}

func TestRunSchedulerFailsAfterMaxAttemptsAndSkipsDownstream(t *testing.T) {
	driver := newFakeDriver()
	reporter := &fakeReporter{}
	rs := newScheduler(t, twoStepYAML, driver, reporter, 1, 0)
	rs.Start()

	waitCond(t, func() bool { return driver.startCount("a") == 1 })
	driver.complete("a", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusFailed, Error: "boom"}})

	waitCond(t, func() bool { return len(reporter.finalizeCalls()) == 1 })
	if got := reporter.finalizeCalls()[0].status; got != run.StatusFailed {
		t.Fatalf("finalize status = %q, want %q", got, run.StatusFailed)
	}
	if driver.startCount("b") != 0 {
		t.Fatal("step b (depends on failed step a) was started, want skipped")
	}
	bSteps := reporter.stepsFor("b")
	if len(bSteps) == 0 || bSteps[len(bSteps)-1].Status != run.StepStatusSkipped {
		t.Fatalf("step b reported status = %#v, want skipped", bSteps)
	}
}

func TestRunSchedulerTimeoutFailsStep(t *testing.T) {
	driver := newFakeDriver()
	reporter := &fakeReporter{}
	rs := newScheduler(t, oneStepTimeoutYAML, driver, reporter, 1, 0)
	rs.Start()

	waitCond(t, func() bool { return driver.startCount("a") == 1 })
	// Never call driver.complete — the step's own 1s options.timeout must
	// fire and fail it via the step's context deadline.

	waitCond(t, func() bool { return len(reporter.finalizeCalls()) == 1 })
	if got := reporter.finalizeCalls()[0].status; got != run.StatusFailed {
		t.Fatalf("finalize status = %q, want %q", got, run.StatusFailed)
	}
	steps := reporter.stepsFor("a")
	if len(steps) == 0 || steps[len(steps)-1].Error != "task execution timeout" {
		t.Fatalf("final step error = %#v, want %q", steps, "task execution timeout")
	}
	if driver.stops["a"] != 1 {
		t.Fatalf("driver.Stop called %d times for timed-out step, want 1", driver.stops["a"])
	}
}

func TestRunSchedulerCancelStopsActiveStepsAndFinalizesCanceled(t *testing.T) {
	driver := newFakeDriver()
	reporter := &fakeReporter{}
	rs := newScheduler(t, oneStepYAML, driver, reporter, 1, 0)
	rs.Start()

	waitCond(t, func() bool { return driver.startCount("a") == 1 })

	if err := rs.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if driver.stops["a"] != 1 {
		t.Fatalf("driver.Stop called %d times after Cancel, want 1", driver.stops["a"])
	}
	calls := reporter.finalizeCalls()
	if len(calls) != 1 || calls[0].status != run.StatusCanceled {
		t.Fatalf("finalize calls = %#v, want exactly one with status %q", calls, run.StatusCanceled)
	}
	steps := reporter.stepsFor("a")
	if len(steps) == 0 || steps[len(steps)-1].Status != run.StepStatusCanceled {
		t.Fatalf("final step status = %#v, want canceled", steps)
	}

	// Cancel is idempotent — a second call must not finalize twice.
	if err := rs.Cancel(); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
	if len(reporter.finalizeCalls()) != 1 {
		t.Fatalf("finalize called again by a second Cancel: %#v", reporter.finalizeCalls())
	}
}

func TestRunSchedulerBuildTaskRejectsInvalidPipelineYAML(t *testing.T) {
	_, err := New(proto.RunDispatch{
		ProjectID:    "proj-1",
		RunID:        "run-1",
		PipelineYAML: "not: valid: pipeline",
	}, Options{
		Driver:        newFakeDriver(),
		Report:        &fakeReporter{},
		BuildExecSpec: noopExecSpec,
	})
	if err == nil {
		t.Fatal("New with invalid pipeline YAML should error")
	}
}

func TestRunSchedulerNewRequiresDependencies(t *testing.T) {
	base := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: oneStepYAML}
	if _, err := New(base, Options{Report: &fakeReporter{}, BuildExecSpec: noopExecSpec}); err == nil {
		t.Fatal("New without a Driver should error")
	}
	if _, err := New(base, Options{Driver: newFakeDriver(), BuildExecSpec: noopExecSpec}); err == nil {
		t.Fatal("New without a Reporter should error")
	}
	if _, err := New(base, Options{Driver: newFakeDriver(), Report: &fakeReporter{}}); err == nil {
		t.Fatal("New without BuildExecSpec should error")
	}
}

func TestMakeTaskID(t *testing.T) {
	if got, want := MakeTaskID("run-1", "step-a"), "run-1:step-a"; got != want {
		t.Fatalf("MakeTaskID = %q, want %q", got, want)
	}
}
