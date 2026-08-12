package directworker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

// fakeDriver mirrors pkg/pipeline/worker's white-box test double for the
// same pdriver.Driver interface, plus a recoverFn hook this package's
// Observe() needs to exercise.
type fakeDriver struct {
	mu       sync.Mutex
	stopErrs map[string]error

	startFn   func(ctx context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error)
	waitFn    func(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error)
	stopFn    func(ctx context.Context, handle pdriver.Handle) error
	recoverFn func(ctx context.Context) ([]pdriver.Handle, error)

	stopCalls []pdriver.Handle
}

func (d *fakeDriver) Start(ctx context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error) {
	if d.startFn != nil {
		return d.startFn(ctx, task, spec)
	}
	return pdriver.Handle{}, fmt.Errorf("not implemented")
}

func (d *fakeDriver) Wait(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error) {
	if d.waitFn != nil {
		return d.waitFn(ctx, handle)
	}
	return pdriver.Exit{}, fmt.Errorf("not implemented")
}

func (d *fakeDriver) Stop(ctx context.Context, handle pdriver.Handle, _ time.Duration) error {
	d.mu.Lock()
	d.stopCalls = append(d.stopCalls, handle)
	stopFn := d.stopFn
	err := d.stopErrs[handle.RuntimeKey]
	d.mu.Unlock()
	if stopFn != nil {
		return stopFn(ctx, handle)
	}
	return err
}

func (d *fakeDriver) stopCallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.stopCalls)
}

func (d *fakeDriver) Recover(ctx context.Context) ([]pdriver.Handle, error) {
	if d.recoverFn != nil {
		return d.recoverFn(ctx)
	}
	return nil, nil
}

func newTestWorker(t *testing.T, driver pdriver.Driver, concurrency int) *Worker {
	t.Helper()
	w, err := New(Config{
		WorkerID:    "test-worker",
		Driver:      driver,
		Concurrency: concurrency,
		ResolveStorage: func(task *proto.Task) (string, string) {
			return task.StorageURL, task.StorageToken
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w
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

func TestNewRequiresConcurrencyAndResolveStorage(t *testing.T) {
	if _, err := New(Config{WorkerID: "w", Driver: &fakeDriver{}}); err == nil {
		t.Fatal("expected error when Concurrency is unset")
	}
	if _, err := New(Config{WorkerID: "w", Driver: &fakeDriver{}, Concurrency: 1}); err == nil {
		t.Fatal("expected error when ResolveStorage is unset")
	}
}

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
	w := newTestWorker(t, driver, 1)

	task1 := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "a", Attempt: 1}
	task2 := &proto.Task{ProjectID: "p", RunID: "run-2", StepName: "b", Attempt: 1}

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = w.Dispatch(context.Background(), task1) }()
	go func() { defer wg.Done(); results[1] = w.Dispatch(context.Background(), task2) }()

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
	w := newTestWorker(t, driver, 4)
	task := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "a", Attempt: 1}

	if err := w.Dispatch(context.Background(), task); err == nil {
		t.Fatal("expected Dispatch to return the Start error")
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
	w := newTestWorker(t, driver, 4)
	task := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "a", Attempt: 1}

	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- w.Dispatch(context.Background(), task) }()

	select {
	case <-startEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("driver.Start was never called")
	}

	if err := w.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelRun during Start returned error: %v", err)
	}

	select {
	case <-dispatchDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch did not return after CancelRun interrupted Start")
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
	if got := driver.stopCallCount(); got != 0 {
		t.Fatalf("driver.Stop call count = %d, want 0 (nothing to stop before Start returned)", got)
	}
}

func TestCancelRunJoinsDriverStopErrors(t *testing.T) {
	driver := &fakeDriver{stopErrs: map[string]error{
		"key-a": fmt.Errorf("stop key-a failed"),
		"key-b": nil,
	}}
	w := &Worker{
		cfg: Config{Driver: driver},
		active: map[string]*trackedTask{
			"key-a": {runID: "run-1", handle: pdriver.Handle{RuntimeKey: "key-a", RunID: "run-1"}, cancel: func() {}},
			"key-b": {runID: "run-1", handle: pdriver.Handle{RuntimeKey: "key-b", RunID: "run-1"}, cancel: func() {}},
			"key-c": {runID: "run-other", handle: pdriver.Handle{RuntimeKey: "key-c", RunID: "run-other"}, cancel: func() {}},
		},
	}

	err := w.CancelRun(context.Background(), "run-1")
	if err == nil {
		t.Fatal("expected CancelRun to return the driver.Stop error, got nil")
	}

	w.mu.Lock()
	_, stillActiveOther := w.active["key-c"]
	w.mu.Unlock()
	if !stillActiveOther {
		t.Fatal("CancelRun must not touch tracked tasks for other runs")
	}
}

// TestObserveRecoversAndReportsResult is new coverage this package's Observe
// needs — internal/k8sworker/pipeline/worker_test.go has no equivalent
// Observe/Recover-on-startup test to mirror.
func TestObserveRecoversAndReportsResult(t *testing.T) {
	waitCh := make(chan pdriver.Exit, 1)
	recovered := pdriver.Handle{RuntimeKey: "key-a", TaskID: "run-1:step", RunID: "run-1", StepName: "step", Attempt: 1}
	driver := &fakeDriver{
		recoverFn: func(context.Context) ([]pdriver.Handle, error) {
			return []pdriver.Handle{recovered}, nil
		},
		waitFn: func(ctx context.Context, _ pdriver.Handle) (pdriver.Exit, error) {
			select {
			case exit := <-waitCh:
				return exit, nil
			case <-ctx.Done():
				return pdriver.Exit{}, ctx.Err()
			}
		},
	}

	var reported atomic.Pointer[proto.TaskResult]
	w := newTestWorker(t, driver, 1)
	w.cfg.ReportResult = func(result proto.TaskResult) error {
		reported.Store(&result)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Observe(ctx)

	if !waitUntil(2*time.Second, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		_, ok := w.active["key-a"]
		return ok
	}) {
		t.Fatal("recovered handle was not registered")
	}

	waitCh <- pdriver.Exit{Result: &proto.TaskResult{TaskID: recovered.TaskID, Status: proto.TaskStatusDone, Attempt: 1}}

	if !waitUntil(2*time.Second, func() bool { return reported.Load() != nil }) {
		t.Fatal("ReportResult was not called after recovered handle completed")
	}
	got := reported.Load()
	if got.TaskID != recovered.TaskID || got.WorkerID != "test-worker" {
		t.Fatalf("reported result = %#v", got)
	}
}
