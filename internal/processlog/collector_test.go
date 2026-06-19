package processlog

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type captureSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *captureSink) Append(_, _, _, line string, _ time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
}
func (s *captureSink) Stop() {}
func (s *captureSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func TestStartCollectorCollectsLinesAndStopIsIdempotent(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "proc.log")
	if err := os.WriteFile(logFile, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &captureSink{}
	stop := StartCollector(logFile, "nb:test", "runtime", sink)
	deadline := time.Now().Add(5 * time.Second)
	for len(sink.snapshot()) < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	stop()
	got := sink.snapshot()
	if len(got) < 3 {
		t.Fatalf("collected %d lines, want 3; got %v", len(got), got)
	}
}

func TestStartCollectorCollectsAppendedLines(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "append.log")
	f, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sink := &captureSink{}
	stop := StartCollector(logFile, "nb:append", "runtime", sink)
	defer stop()
	if _, err := f.WriteString("first\nsecond\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(sink.snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := sink.snapshot(); len(got) < 2 {
		t.Fatalf("collected %d lines, want at least 2; got %v", len(got), got)
	}
}

func TestStartCollectorMissingFileDoesNotPanic(t *testing.T) {
	stop := StartCollector(filepath.Join(t.TempDir(), "missing.log"), "nb:x", "runtime", &captureSink{})
	stop()
	stop()
}
