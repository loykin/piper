package retention

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTTLPurge_DisabledWhenTTLNotPositive(t *testing.T) {
	called := false
	purge := func(ctx context.Context, cutoff time.Time) (int64, error) {
		called = true
		return 0, nil
	}

	for _, ttl := range []time.Duration{0, -time.Second} {
		called = false
		TTLPurge("x", ttl, purge).Run(context.Background())
		if called {
			t.Fatalf("ttl=%v: purge should not be called when disabled", ttl)
		}
	}
}

func TestTTLPurge_ComputesCutoffFromTTL(t *testing.T) {
	ttl := 24 * time.Hour
	var gotCutoff time.Time
	before := time.Now().UTC().Add(-ttl)

	TTLPurge("x", ttl, func(ctx context.Context, cutoff time.Time) (int64, error) {
		gotCutoff = cutoff
		return 3, nil
	}).Run(context.Background())

	after := time.Now().UTC().Add(-ttl)
	if gotCutoff.Before(before) || gotCutoff.After(after) {
		t.Fatalf("cutoff %v not within expected window [%v, %v]", gotCutoff, before, after)
	}
}

func TestTTLPurge_ErrorDoesNotPanic(t *testing.T) {
	job := TTLPurge("x", time.Hour, func(ctx context.Context, cutoff time.Time) (int64, error) {
		return 0, errors.New("boom")
	})
	job.Run(context.Background())
}

func TestRunner_RunsAllJobsInOrder(t *testing.T) {
	var order []string
	r := NewRunner()
	r.Register("a", JobFunc(func(ctx context.Context) { order = append(order, "a") }))
	r.Register("b", JobFunc(func(ctx context.Context) { order = append(order, "b") }))
	r.Register("c", JobFunc(func(ctx context.Context) { order = append(order, "c") }))

	r.Run(context.Background())

	want := []string{"a", "b", "c"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("got %v, want %v", order, want)
		}
	}
}

func TestRunner_OneJobFailingDoesNotStopOthers(t *testing.T) {
	var ran []string
	r := NewRunner()
	r.Register("failing", TTLPurge("failing", time.Hour, func(ctx context.Context, cutoff time.Time) (int64, error) {
		ran = append(ran, "failing")
		return 0, errors.New("boom")
	}))
	r.Register("ok", JobFunc(func(ctx context.Context) { ran = append(ran, "ok") }))

	r.Run(context.Background())

	if len(ran) != 2 || ran[0] != "failing" || ran[1] != "ok" {
		t.Fatalf("expected both jobs to run, got %v", ran)
	}
}
