package pipelineworker

import (
	"context"
	"errors"
	"testing"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

func TestDispatchRejectsNewWorkWhileDraining(t *testing.T) {
	driver := &fakeDriver{
		startFn: func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error) {
			t.Fatal("driver.Start must not be called once draining")
			return pdriver.Handle{}, nil
		},
	}
	w := newTestWorker(driver)
	w.mu.Lock()
	w.draining = true
	w.mu.Unlock()

	task := &proto.Task{ProjectID: "p", RunID: "run-1", StepName: "a", Attempt: 1}
	err := w.dispatch(context.Background(), task)

	var busy *iagent.BusyError
	if err == nil {
		t.Fatal("expected dispatch to reject work while draining, got nil error")
	}
	if !errors.As(err, &busy) {
		t.Fatalf("dispatch error = %v, want *iagent.BusyError", err)
	}

	w.mu.Lock()
	inFlight := w.inFlight
	activeCount := len(w.active)
	w.mu.Unlock()
	if inFlight != 0 || activeCount != 0 {
		t.Fatalf("dispatch must not reserve capacity while draining: inFlight=%d active=%d", inFlight, activeCount)
	}
}

// TestShutdownRespectsOverallBudgetAcrossMultipleTasks verifies that
// shutdown(ctx) does not wait longer than the ctx's own deadline even when
// multiple in-flight jobs' Stop calls would otherwise block indefinitely —
// each Stop call must observe the same shared ctx and return promptly when
// it expires, so the overall shutdown() call is bounded by one grace period
// rather than accumulating per-task.
func TestShutdownRespectsOverallBudgetAcrossMultipleTasks(t *testing.T) {
	driver := &fakeDriver{
		stopFn: func(ctx context.Context, _ pdriver.Handle) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	w := newTestWorker(driver)

	const numTasks = 5
	w.mu.Lock()
	for i := 0; i < numTasks; i++ {
		key := "run-" + string(rune('a'+i))
		w.active[key] = &trackedTask{
			runID:  key,
			handle: pdriver.Handle{RuntimeKey: key, RunID: key},
			cancel: func() {},
		}
	}
	w.inFlight = numTasks
	w.mu.Unlock()

	budget := 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	start := time.Now()
	w.shutdown(ctx)
	elapsed := time.Since(start)

	if elapsed > budget+500*time.Millisecond {
		t.Fatalf("shutdown took %v, want close to the %v budget (not accumulated per task)", elapsed, budget)
	}

	w.mu.Lock()
	draining := w.draining
	w.mu.Unlock()
	if !draining {
		t.Fatal("shutdown must mark the worker draining")
	}
}
