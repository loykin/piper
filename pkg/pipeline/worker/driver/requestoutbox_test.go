package driver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type outboxTestPayload struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

func TestRequestOutboxRetriesUntilSendSucceeds(t *testing.T) {
	var failUntil int32 = 2
	var attempts int32
	var mu sync.Mutex
	var delivered []outboxTestPayload

	outbox, err := NewRequestOutbox(t.TempDir(), func(_ context.Context, method string, payload json.RawMessage) error {
		n := atomic.AddInt32(&attempts, 1)
		if n <= failUntil {
			return errors.New("tunnel down")
		}
		var p outboxTestPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		mu.Lock()
		delivered = append(delivered, p)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := outbox.Enqueue("run-1:finalize", "pipeline.run_finalize", outboxTestPayload{RunID: "run-1", Status: "success"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go outbox.Run(ctx)

	waitForOutboxCond(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) == 1
	})
	mu.Lock()
	got := delivered[0]
	mu.Unlock()
	if got.RunID != "run-1" || got.Status != "success" {
		t.Fatalf("delivered payload = %#v", got)
	}

	// Once delivered, the entry must not be redelivered on the next tick.
	time.Sleep(2300 * time.Millisecond)
	mu.Lock()
	after := len(delivered)
	mu.Unlock()
	if after != 1 {
		t.Fatalf("entry redelivered after success: got %d deliveries, want 1", after)
	}
}

func TestRequestOutboxReplaysPersistedEntryAfterRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := NewRequestOutbox(dir, func(context.Context, string, json.RawMessage) error {
		return errors.New("tunnel down")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Enqueue("run-1:step:1:running", "pipeline.step_upsert", outboxTestPayload{RunID: "run-1", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	delivered := make(chan outboxTestPayload, 1)
	restarted, err := NewRequestOutbox(dir, func(_ context.Context, method string, payload json.RawMessage) error {
		if method != "pipeline.step_upsert" {
			t.Errorf("method = %q, want pipeline.step_upsert", method)
		}
		var p outboxTestPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		select {
		case delivered <- p:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go restarted.Run(ctx)

	select {
	case p := <-delivered:
		if p.RunID != "run-1" {
			t.Fatalf("RunID = %q", p.RunID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("persisted request was not replayed after restart")
	}
}

func TestRequestOutboxEnqueueRequiresID(t *testing.T) {
	outbox, err := NewRequestOutbox(t.TempDir(), func(context.Context, string, json.RawMessage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Enqueue("", "pipeline.run_finalize", outboxTestPayload{}); err == nil {
		t.Fatal("Enqueue with empty id should error")
	}
}

func waitForOutboxCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}
