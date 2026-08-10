package scheduler

import (
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

func newTestRegistry(driver *fakeDriver, reporters map[string]*fakeReporter) *Registry {
	return NewRegistry(RegistryOptions{
		Driver:        driver,
		BuildExecSpec: noopExecSpec,
		BuildReporter: func(_, runID string) StepReporter {
			r := &fakeReporter{}
			reporters[runID] = r
			return r
		},
		MaxAttempts: 1,
		WorkerID:    "worker-1",
	})
}

func TestRegistryStartRunIsIdempotent(t *testing.T) {
	driver := newFakeDriver()
	reporters := make(map[string]*fakeReporter)
	reg := newTestRegistry(driver, reporters)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: oneStepYAML}
	if err := reg.StartRun(dispatch); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitCond(t, func() bool { return driver.startCount("a") == 1 })

	// A second StartRun for the same still-active RunID must be a no-op —
	// this is what lets the master's at-least-once resend (see the Phase
	// 2/3 recovery design) be safe.
	if err := reg.StartRun(dispatch); err != nil {
		t.Fatalf("StartRun (duplicate): %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if driver.startCount("a") != 1 {
		t.Fatalf("step a started %d times after duplicate StartRun, want 1", driver.startCount("a"))
	}
	if reg.Len() != 1 {
		t.Fatalf("Registry.Len() = %d, want 1", reg.Len())
	}

	driver.complete("a", pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusDone}})
	waitCond(t, func() bool { return reg.Len() == 0 })

	// Once finalized and dropped, a fresh StartRun for the same RunID must
	// be allowed to run again (e.g. a legitimate resubmission), not
	// silently swallowed as if it were still active.
	if err := reg.StartRun(dispatch); err != nil {
		t.Fatalf("StartRun (after finalize): %v", err)
	}
	waitCond(t, func() bool { return driver.startCount("a") == 2 })
}

func TestRegistryCancelRunNoOpForUntrackedRun(t *testing.T) {
	driver := newFakeDriver()
	reporters := make(map[string]*fakeReporter)
	reg := newTestRegistry(driver, reporters)

	if err := reg.CancelRun("nonexistent-run"); err != nil {
		t.Fatalf("CancelRun for an untracked run should be a no-op, got err: %v", err)
	}
}

func TestRegistryCancelRunStopsTrackedRun(t *testing.T) {
	driver := newFakeDriver()
	reporters := make(map[string]*fakeReporter)
	reg := newTestRegistry(driver, reporters)

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-1", PipelineYAML: oneStepYAML}
	if err := reg.StartRun(dispatch); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitCond(t, func() bool { return driver.startCount("a") == 1 })

	if err := reg.CancelRun("run-1"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	waitCond(t, func() bool { return reg.Len() == 0 })
	if driver.stops["a"] != 1 {
		t.Fatalf("driver.Stop called %d times, want 1", driver.stops["a"])
	}
}
