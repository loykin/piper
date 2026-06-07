package taskruntime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/piper/piper/pkg/proto"
)

func TestResultOutboxKeepsResultUntilAck(t *testing.T) {
	var mu sync.Mutex
	var delivered []proto.TaskResult
	outbox, err := NewResultOutbox(t.TempDir(), func(result proto.TaskResult) error {
		mu.Lock()
		delivered = append(delivered, result)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := proto.TaskResult{TaskID: "run:step", Attempt: 2, Status: proto.TaskStatusDone}
	if err := outbox.Enqueue(result); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go outbox.Run(ctx)

	waitForOutbox(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) >= 2
	})
	if err := outbox.Ack(ResultAck{TaskID: result.TaskID, Attempt: result.Attempt}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	before := len(delivered)
	mu.Unlock()
	time.Sleep(2300 * time.Millisecond)
	mu.Lock()
	after := len(delivered)
	mu.Unlock()
	if after != before {
		t.Fatalf("deliveries continued after ack: before=%d after=%d", before, after)
	}
}

func TestResultOutboxReplaysPersistedResultAfterRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := NewResultOutbox(dir, func(proto.TaskResult) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Enqueue(proto.TaskResult{TaskID: "run:step", Attempt: 1, Status: proto.TaskStatusDone}); err != nil {
		t.Fatal(err)
	}

	delivered := make(chan proto.TaskResult, 1)
	restarted, err := NewResultOutbox(dir, func(result proto.TaskResult) error {
		select {
		case delivered <- result:
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
	case result := <-delivered:
		if result.TaskID != "run:step" {
			t.Fatalf("task ID = %q", result.TaskID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("persisted result was not replayed")
	}
}

func waitForOutbox(t *testing.T, cond func() bool) {
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
