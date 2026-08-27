package statsstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryBackend struct {
	mu      sync.Mutex
	fail    bool
	logs    map[string]LogLine
	metrics map[string]MetricPoint
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{logs: map[string]LogLine{}, metrics: map[string]MetricPoint{}}
}
func (b *memoryBackend) AppendLogs(_ context.Context, lines []LogLine) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return errors.New("offline")
	}
	for _, line := range lines {
		b.logs[line.EventID] = line
	}
	return nil
}
func (b *memoryBackend) AppendMetrics(_ context.Context, points []MetricPoint) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return errors.New("offline")
	}
	for _, point := range points {
		b.metrics[point.EventID] = point
	}
	return nil
}
func (b *memoryBackend) QueryLogs(_ context.Context, q LogQuery) (LogPage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return LogPage{}, errors.New("offline")
	}
	lines := make([]LogLine, 0, len(b.logs))
	for _, line := range b.logs {
		if matchLog(line, q) {
			lines = append(lines, line)
		}
	}
	return logPageFrom(lines, q), nil
}
func (b *memoryBackend) QueryMetrics(_ context.Context, q MetricQuery) (MetricPage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return MetricPage{}, errors.New("offline")
	}
	points := make([]MetricPoint, 0, len(b.metrics))
	for _, point := range b.metrics {
		if matchMetric(point, q) {
			points = append(points, point)
		}
	}
	return metricPageFrom(points, q), nil
}

func TestSpoolRecoversMergesAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	backend := newMemoryBackend()
	backend.fail = true
	spool, err := openDiskSpool(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := newSpooledBackend(backend, backend, spool, true, true)
	now := time.Now().UTC()
	if err := wrapped.AppendLogs(context.Background(), []LogLine{{ProjectID: "p", RunID: "r", StepName: "s", Ts: now, Line: "queued"}}); err != nil {
		t.Fatal(err)
	}
	page, err := wrapped.QueryLogs(context.Background(), LogQuery{ProjectID: "p", RunID: "r", StepName: "s"})
	if err != nil || len(page.Lines) != 1 || page.Lines[0].EventID == "" || page.Lines[0].ID == 0 {
		t.Fatalf("merged page=%+v err=%v", page, err)
	}
	eventID := page.Lines[0].EventID
	id := page.Lines[0].ID
	wrapped.close()
	backend.fail = false
	reopened, err := openDiskSpool(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	replay := newSpooledBackend(backend, backend, reopened, true, true)
	defer replay.close()
	replay.signal()
	eventually(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.logs) == 1 })
	page, err = replay.QueryLogs(context.Background(), LogQuery{ProjectID: "p", RunID: "r", StepName: "s"})
	if err != nil || len(page.Lines) != 1 || page.Lines[0].EventID != eventID || page.Lines[0].ID != id {
		t.Fatalf("replayed page=%+v err=%v", page, err)
	}
	if err := replay.AppendLogs(context.Background(), []LogLine{{ProjectID: "p", RunID: "r", StepName: "s", Ts: now, Line: "next"}}); err != nil {
		t.Fatal(err)
	}
	next, _ := replay.QueryLogs(context.Background(), LogQuery{ProjectID: "p", RunID: "r", StepName: "s"})
	if len(next.Lines) != 2 || next.Lines[1].ID <= id {
		t.Fatalf("sequence did not survive restart: %+v", next.Lines)
	}
}

func TestSpoolFullIsExplicitAndDegraded(t *testing.T) {
	spool, err := openDiskSpool(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	backend := newMemoryBackend()
	backend.fail = true
	wrapped := newSpooledBackend(backend, backend, spool, true, false)
	defer wrapped.close()
	err = wrapped.AppendLogs(context.Background(), []LogLine{{ProjectID: "p", RunID: "r", StepName: "s", Ts: time.Now(), Line: "too large"}})
	if !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("error=%v", err)
	}
	health := wrapped.health()
	if !health.Degraded || health.LastError != "statistics spool is full" {
		t.Fatalf("health=%+v", health)
	}
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}
