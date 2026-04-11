package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"
)

func makeRunner(steps []Step, execFn StepExecutorFunc) *Runner {
	p := &Pipeline{Spec: Spec{Steps: steps}}
	dag, _ := BuildDAG(p)
	cfg := RunnerConfig{MaxRetries: 0, RetryDelay: 0, Concurrency: 0}
	return NewRunner(p, dag, cfg, execFn)
}

func TestRunner_allSuccess(t *testing.T) {
	ss := []Step{
		{Name: "a", DependsOn: []string{}},
		{Name: "b", DependsOn: []string{"a"}},
	}
	r := makeRunner(ss, func(_ context.Context, s *Step) error { return nil })
	result := r.Run(context.Background())

	if result.Failed() {
		t.Fatal("expected success")
	}
	for _, name := range []string{"a", "b"} {
		if result.Steps[name].Status != StatusDone {
			t.Errorf("step %s: want done, got %s", name, result.Steps[name].Status)
		}
	}
}

func TestRunner_stepFail_skipsDownstream(t *testing.T) {
	ss := []Step{
		{Name: "a", DependsOn: []string{}},
		{Name: "b", DependsOn: []string{"a"}},
		{Name: "c", DependsOn: []string{"b"}},
	}
	r := makeRunner(ss, func(_ context.Context, s *Step) error {
		if s.Name == "a" {
			return errors.New("fail")
		}
		return nil
	})
	result := r.Run(context.Background())

	if !result.Failed() {
		t.Fatal("expected failure")
	}
	if result.Steps["a"].Status != StatusFailed {
		t.Errorf("a should be failed")
	}
	if result.Steps["b"].Status != StatusSkipped {
		t.Errorf("b should be skipped, got %s", result.Steps["b"].Status)
	}
	if result.Steps["c"].Status != StatusSkipped {
		t.Errorf("c should be skipped, got %s", result.Steps["c"].Status)
	}
}

func TestRunner_retry(t *testing.T) {
	calls := 0
	ss := []Step{{Name: "a", DependsOn: []string{}}}
	p := &Pipeline{Spec: Spec{Steps: ss}}
	dag, _ := BuildDAG(p)
	cfg := RunnerConfig{MaxRetries: 2, RetryDelay: time.Millisecond, Concurrency: 0}
	r := NewRunner(p, dag, cfg, func(_ context.Context, s *Step) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})

	result := r.Run(context.Background())
	if result.Failed() {
		t.Fatal("expected success after retries")
	}
	if calls != 3 {
		t.Errorf("want 3 calls (1 initial + 2 retries), got %d", calls)
	}
	if result.Steps["a"].Attempts != 3 {
		t.Errorf("want attempts=3, got %d", result.Steps["a"].Attempts)
	}
}

func TestRunner_retryExhausted(t *testing.T) {
	ss := []Step{{Name: "a", DependsOn: []string{}}}
	p := &Pipeline{Spec: Spec{Steps: ss}}
	dag, _ := BuildDAG(p)
	cfg := RunnerConfig{MaxRetries: 1, RetryDelay: time.Millisecond, Concurrency: 0}
	r := NewRunner(p, dag, cfg, func(_ context.Context, s *Step) error {
		return errors.New("always fails")
	})

	result := r.Run(context.Background())
	if !result.Failed() {
		t.Fatal("expected failure")
	}
	if result.Steps["a"].Attempts != 2 {
		t.Errorf("want attempts=2, got %d", result.Steps["a"].Attempts)
	}
}

func TestRunner_parallel(t *testing.T) {
	// a and b run in parallel; c runs after both complete
	started := make(chan string, 3)
	ss := []Step{
		{Name: "a", DependsOn: []string{}},
		{Name: "b", DependsOn: []string{}},
		{Name: "c", DependsOn: []string{"a", "b"}},
	}
	r := makeRunner(ss, func(_ context.Context, s *Step) error {
		started <- s.Name
		return nil
	})

	result := r.Run(context.Background())
	if result.Failed() {
		t.Fatal("expected success")
	}
	close(started)
	var order []string
	for n := range started {
		order = append(order, n)
	}
	if order[len(order)-1] != "c" {
		t.Errorf("c must run last, order: %v", order)
	}
}

func TestRunner_contextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ss := []Step{{Name: "a", DependsOn: []string{}}}
	p := &Pipeline{Spec: Spec{Steps: ss}}
	dag, _ := BuildDAG(p)
	cfg := RunnerConfig{MaxRetries: 0, RetryDelay: 0}
	r := NewRunner(p, dag, cfg, func(ctx context.Context, s *Step) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})

	result := r.Run(ctx)
	if !result.Failed() {
		t.Fatal("expected failure on cancel")
	}
}
