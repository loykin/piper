package pipelineworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

func TestDispatchReservesCapacityAtomicallyUnderConcurrency(t *testing.T) {
	release := make(chan struct{})
	var startCount atomic.Int32
	driver := &fakeDriver{
		startFn: func(_ context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error) {
			startCount.Add(1)
			<-release
			return pdriver.Handle{RuntimeKey: spec.RuntimeKey, RunID: task.RunID}, nil
		},
		waitFn: func(ctx context.Context, _ pdriver.Handle) (pdriver.Exit, error) {
			<-ctx.Done()
			return pdriver.Exit{}, ctx.Err()
		},
	}
	w := newTestWorker(driver)
	w.cfg.Agent.Concurrency = 1

	task1 := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "a", Attempt: 1}
	task2 := &proto.Task{ProjectID: "p", RunID: "run-2", StepName: "b", Attempt: 1}

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = w.dispatch(context.Background(), task1) }()
	go func() { defer wg.Done(); results[1] = w.dispatch(context.Background(), task2) }()

	// Give both goroutines a chance to race the capacity check before letting
	// the one that got in finish Start.
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()

	busyCount, okCount := 0, 0
	for _, err := range results {
		var busy *iagent.BusyError
		switch {
		case err == nil:
			okCount++
		case errors.As(err, &busy):
			busyCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || busyCount != 1 {
		t.Fatalf("okCount=%d busyCount=%d, want exactly one ok and one busy", okCount, busyCount)
	}
	if got := startCount.Load(); got != 1 {
		t.Fatalf("driver.Start called %d times, want exactly 1 (capacity=1 must not be exceeded)", got)
	}

	w.mu.Lock()
	for _, tt := range w.active {
		tt.cancel()
	}
	w.mu.Unlock()
}

func TestDispatchDecrementsInFlightOnStartFailure(t *testing.T) {
	driver := &fakeDriver{
		startFn: func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error) {
			return pdriver.Handle{}, errors.New("start failed")
		},
	}
	w := newTestWorker(driver)
	task := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "a", Attempt: 1}

	if err := w.dispatch(context.Background(), task); err == nil {
		t.Fatal("expected dispatch to return the Start error")
	}

	w.mu.Lock()
	inFlight := w.inFlight
	activeCount := len(w.active)
	w.mu.Unlock()
	if inFlight != 0 {
		t.Fatalf("inFlight = %d, want 0 after Start failure", inFlight)
	}
	if activeCount != 0 {
		t.Fatalf("active entries = %d, want 0 after Start failure", activeCount)
	}
}

func TestCancelDuringStartInterruptsBeforeHandleRegistered(t *testing.T) {
	startEntered := make(chan struct{})
	driver := &fakeDriver{
		startFn: func(ctx context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error) {
			close(startEntered)
			<-ctx.Done()
			return pdriver.Handle{}, ctx.Err()
		},
	}
	w := newTestWorker(driver)
	task := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "a", Attempt: 1}

	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- w.dispatch(context.Background(), task) }()

	select {
	case <-startEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("driver.Start was never called")
	}

	if err := w.cancelRun("run-1"); err != nil {
		t.Fatalf("cancelRun during Start returned error: %v", err)
	}

	// dispatch() returning an error here is expected and harmless (the
	// interrupted Start surfaces as a failed RPC response) — the run is
	// already confirmed canceled queue-side by the time this races. What
	// matters is that dispatch returns promptly and cleans up, not the
	// specific error value.
	select {
	case <-dispatchDone:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not return after cancelRun interrupted Start")
	}

	w.mu.Lock()
	activeCount := len(w.active)
	inFlight := w.inFlight
	w.mu.Unlock()
	if activeCount != 0 {
		t.Fatalf("active entries = %d, want 0 after canceled mid-start", activeCount)
	}
	if inFlight != 0 {
		t.Fatalf("inFlight = %d, want 0 after canceled mid-start", inFlight)
	}
	// Nothing had a handle yet, so cancelRun must not have called driver.Stop.
	if got := driver.stopCallCount(); got != 0 {
		t.Fatalf("driver.Stop call count = %d, want 0 (nothing to stop before Start returned)", got)
	}
}
