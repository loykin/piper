package pipelinedispatch

import (
	"context"
	"time"

	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

// fakeDirectDriver is a minimal pdriver.Driver double shared by
// DockerBackend/BaremetalBackend tests, which can't use a real Docker daemon
// or spawn real subprocesses the way the driver-level contract tests
// (pkg/pipeline/worker/driver/{docker,baremetal}) do.
type fakeDirectDriver struct {
	startFn func(ctx context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error)
	waitFn  func(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error)
	stopFn  func(ctx context.Context, handle pdriver.Handle) error
}

func (d *fakeDirectDriver) Start(ctx context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error) {
	if d.startFn != nil {
		return d.startFn(ctx, task, spec)
	}
	return pdriver.Handle{RuntimeKey: spec.RuntimeKey, RunID: task.RunID, TaskID: task.ID}, nil
}

func (d *fakeDirectDriver) Wait(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error) {
	if d.waitFn != nil {
		return d.waitFn(ctx, handle)
	}
	<-ctx.Done()
	return pdriver.Exit{}, ctx.Err()
}

func (d *fakeDirectDriver) Stop(ctx context.Context, handle pdriver.Handle, _ time.Duration) error {
	if d.stopFn != nil {
		return d.stopFn(ctx, handle)
	}
	return nil
}

func (d *fakeDirectDriver) Recover(context.Context) ([]pdriver.Handle, error) {
	return nil, nil
}

func waitUntilTrue(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
