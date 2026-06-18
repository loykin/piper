package logsink

import (
	"sync"
	"testing"
	"time"
)

type mockPushClient struct {
	mu     sync.Mutex
	pushes []LogAppendPush
}

func (m *mockPushClient) SendPush(_ string, payload any) error {
	p := payload.(LogAppendPush)
	m.mu.Lock()
	m.pushes = append(m.pushes, p)
	m.mu.Unlock()
	return nil
}

func (m *mockPushClient) received() []LogAppendPush {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogAppendPush, len(m.pushes))
	copy(out, m.pushes)
	return out
}

func (m *mockPushClient) totalLines() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, p := range m.pushes {
		n += len(p.Lines)
	}
	return n
}

func TestGRPCLogSink_StopFlushesPendingLines(t *testing.T) {
	client := &mockPushClient{}
	sink := NewGRPCLogSink("proj-1", client)

	now := time.Now()
	sink.Append("run-1", "runtime", "stdout", "line1", now)
	sink.Append("run-1", "runtime", "stdout", "line2", now)
	sink.Stop()

	if got := client.totalLines(); got != 2 {
		t.Fatalf("total lines after Stop = %d, want 2", got)
	}
	for _, p := range client.received() {
		if p.ProjectID != "proj-1" || p.RunID != "run-1" || p.StepName != "runtime" {
			t.Fatalf("unexpected push metadata: %+v", p)
		}
	}
}

func TestGRPCLogSink_FlushesOnTimer(t *testing.T) {
	client := &mockPushClient{}
	sink := NewGRPCLogSink("proj-2", client)
	defer sink.Stop()

	sink.Append("run-2", "runtime", "stdout", "hello", time.Now())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.totalLines() >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("line was not flushed within 3s (expected flush timer to fire)")
}

func TestGRPCLogSink_GroupsByRunIDAndStep(t *testing.T) {
	client := &mockPushClient{}
	sink := NewGRPCLogSink("proj-3", client)

	now := time.Now()
	sink.Append("run-a", "step-1", "stdout", "a1", now)
	sink.Append("run-b", "step-2", "stdout", "b1", now)
	sink.Append("run-a", "step-1", "stdout", "a2", now)
	sink.Stop()

	if client.totalLines() != 3 {
		t.Fatalf("total lines = %d, want 3", client.totalLines())
	}

	counts := map[string]int{}
	for _, p := range client.received() {
		counts[p.RunID] += len(p.Lines)
	}
	if counts["run-a"] != 2 {
		t.Fatalf("run-a lines = %d, want 2", counts["run-a"])
	}
	if counts["run-b"] != 1 {
		t.Fatalf("run-b lines = %d, want 1", counts["run-b"])
	}
}

func TestGRPCLogSink_StopIsIdempotent(t *testing.T) {
	client := &mockPushClient{}
	sink := NewGRPCLogSink("proj-5", client)
	sink.Append("run-5", "runtime", "stdout", "x", time.Now())
	sink.Stop()
	sink.Stop() // must not panic
}

func TestGRPCLogSink_AppendDoesNotBlockWhenFull(t *testing.T) {
	client := &mockPushClient{}
	sink := NewGRPCLogSink("proj-4", client)
	defer sink.Stop()

	done := make(chan struct{})
	go func() {
		now := time.Now()
		for i := 0; i < logSinkBufSize*3; i++ {
			sink.Append("run-4", "runtime", "stdout", "x", now)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked under full buffer")
	}
}
