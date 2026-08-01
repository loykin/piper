package grpcagent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCommandExecutorRunsHandlersConcurrently(t *testing.T) {
	exec := newCommandExecutor(4)
	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	fastDone := make(chan struct{})

	exec.run(func() {
		close(slowStarted)
		<-slowRelease
	})

	<-slowStarted
	exec.run(func() {
		close(fastDone)
	})

	select {
	case <-fastDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fast handler was blocked behind the slow one — commandExecutor is not running handlers concurrently")
	}

	close(slowRelease)
	exec.wait()
}

func TestCommandExecutorAppliesDefaultTimeoutToHungHandler(t *testing.T) {
	exec := newCommandExecutor(4)
	result := make(chan error, 1)

	exec.runBounded(context.Background(), 50*time.Millisecond, func(ctx context.Context) {
		<-ctx.Done()
		result <- ctx.Err()
	})

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ctx.Err() = %v, want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hung handler's context was never bounded")
	}
	exec.wait()
}

func TestCommandExecutorRunBoundedUsesDefaultTimeoutWhenUnset(t *testing.T) {
	exec := newCommandExecutor(4)
	result := make(chan time.Time, 1)

	exec.runBounded(context.Background(), 0, func(ctx context.Context) {
		dl, ok := ctx.Deadline()
		if !ok {
			t.Error("expected a deadline when timeout<=0 falls back to defaultCommandTimeout")
		}
		result <- dl
	})

	select {
	case dl := <-result:
		wantMin := time.Now().Add(defaultCommandTimeout - 5*time.Second)
		wantMax := time.Now().Add(defaultCommandTimeout + 5*time.Second)
		if dl.Before(wantMin) || dl.After(wantMax) {
			t.Fatalf("deadline = %v, want near now+%v", dl, defaultCommandTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never ran")
	}
	exec.wait()
}
