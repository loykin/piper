package pipelineworker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

// fakeDriver is a configurable pdriver.Driver stub for white-box worker
// tests. Zero-value fields fall back to a safe "not implemented" default so
// tests only need to set the hooks they actually exercise.
type fakeDriver struct {
	mu       sync.Mutex
	stopErrs map[string]error // runtimeKey -> error to return from Stop

	startFn func(ctx context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error)
	waitFn  func(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error)
	stopFn  func(ctx context.Context, handle pdriver.Handle) error // overrides stopErrs when set; receives the real ctx

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

func (d *fakeDriver) Recover(context.Context) ([]pdriver.Handle, error) {
	return nil, nil
}

func TestCancelRunJoinsDriverStopErrors(t *testing.T) {
	driver := &fakeDriver{stopErrs: map[string]error{
		"key-a": fmt.Errorf("stop key-a failed"),
		"key-b": nil,
	}}
	w := &Worker{
		driver: driver,
		active: map[string]*trackedTask{
			"key-a": {runID: "run-1", handle: pdriver.Handle{RuntimeKey: "key-a", RunID: "run-1"}, cancel: func() {}},
			"key-b": {runID: "run-1", handle: pdriver.Handle{RuntimeKey: "key-b", RunID: "run-1"}, cancel: func() {}},
			"key-c": {runID: "run-other", handle: pdriver.Handle{RuntimeKey: "key-c", RunID: "run-other"}, cancel: func() {}},
		},
	}

	err := w.cancelRun("run-1")
	if err == nil {
		t.Fatal("expected cancelRun to return the driver.Stop error, got nil")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty joined error")
	}

	w.mu.Lock()
	_, stillActiveOther := w.active["key-c"]
	w.mu.Unlock()
	if !stillActiveOther {
		t.Fatal("cancelRun must not touch tracked tasks for other runs")
	}
}
