package pipelineworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

func newTestWorker(driver pdriver.Driver) *Worker {
	return &Worker{
		cfg: Config{
			Agent: AgentConfig{ID: "test-worker", Concurrency: 4},
		},
		driver: driver,
		active: make(map[string]*trackedTask),
	}
}

func TestDispatchDerivesDeadlineContextFromTaskDeadline(t *testing.T) {
	deadlineCh := make(chan time.Time, 1)
	driver := &fakeDriver{
		startFn: func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error) {
			return pdriver.Handle{RuntimeKey: "run-1-step-1", RunID: "run-1"}, nil
		},
		waitFn: func(ctx context.Context, _ pdriver.Handle) (pdriver.Exit, error) {
			dl, ok := ctx.Deadline()
			if ok {
				deadlineCh <- dl
			} else {
				deadlineCh <- time.Time{}
			}
			<-ctx.Done()
			return pdriver.Exit{}, ctx.Err()
		},
	}
	w := newTestWorker(driver)

	want := time.Now().Add(5 * time.Second)
	task := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "step", Deadline: &want}

	if err := w.dispatch(context.Background(), task); err != nil {
		t.Fatalf("dispatch returned error: %v", err)
	}

	select {
	case got := <-deadlineCh:
		if got.IsZero() {
			t.Fatal("Wait's context has no deadline, want one derived from task.Deadline")
		}
		if diff := got.Sub(want); diff < -time.Second || diff > time.Second {
			t.Fatalf("Wait deadline = %v, want ~%v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait was never called")
	}

	// Unblock the observe goroutine so it doesn't leak past the test.
	w.mu.Lock()
	for _, tt := range w.active {
		tt.cancel()
	}
	w.mu.Unlock()
}

func TestObserveStopsDriverAndReportsTimeoutOnDeadlineExceeded(t *testing.T) {
	driver := &fakeDriver{
		waitFn: func(ctx context.Context, _ pdriver.Handle) (pdriver.Exit, error) {
			<-ctx.Done()
			return pdriver.Exit{}, ctx.Err()
		},
	}
	w := newTestWorker(driver)
	outboxDir := t.TempDir()
	outbox, err := pdriver.NewResultOutbox(outboxDir, func(proto.TaskResult) error { return nil })
	if err != nil {
		t.Fatalf("NewResultOutbox: %v", err)
	}
	w.outbox = outbox

	taskCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(50*time.Millisecond))
	defer cancel()
	handle := pdriver.Handle{RuntimeKey: "run-1-step-1", RunID: "run-1", TaskID: "run-1:step"}
	w.mu.Lock()
	w.active[handle.RuntimeKey] = &trackedTask{handle: handle, cancel: cancel}
	w.inFlight = 1
	w.mu.Unlock()

	w.observe(taskCtx, handle)

	if !errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("taskCtx.Err() = %v, want DeadlineExceeded", taskCtx.Err())
	}
	if got := driver.stopCallCount(); got != 1 {
		t.Fatalf("driver.Stop call count = %d, want 1", got)
	}
	w.mu.Lock()
	_, stillActive := w.active[handle.RuntimeKey]
	inFlight := w.inFlight
	w.mu.Unlock()
	if stillActive {
		t.Fatal("handle should have been removed from w.active after observe returns")
	}
	if inFlight != 0 {
		t.Fatalf("inFlight = %d, want 0", inFlight)
	}
}

func TestObserveReturnsSilentlyOnExplicitCancel(t *testing.T) {
	driver := &fakeDriver{
		waitFn: func(ctx context.Context, _ pdriver.Handle) (pdriver.Exit, error) {
			<-ctx.Done()
			return pdriver.Exit{}, ctx.Err()
		},
	}
	w := newTestWorker(driver)

	taskCtx, cancel := context.WithCancel(context.Background())
	handle := pdriver.Handle{RuntimeKey: "run-1-step-1", RunID: "run-1", TaskID: "run-1:step"}
	w.mu.Lock()
	w.active[handle.RuntimeKey] = &trackedTask{handle: handle, cancel: cancel}
	w.mu.Unlock()

	cancel() // simulate cancelRun()/shutdown() already calling Stop and canceling.
	w.observe(taskCtx, handle)

	// observe must not itself call Stop for a plain (non-deadline) cancel —
	// cancelRun()/shutdown() already own that.
	if got := driver.stopCallCount(); got != 0 {
		t.Fatalf("driver.Stop call count = %d, want 0 (explicit-cancel path must not double-stop)", got)
	}
}
