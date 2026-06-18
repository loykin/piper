package notebookworker

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
	s.lines = append(s.lines, line)
	s.mu.Unlock()
}

func (s *captureSink) Stop() {}

func (s *captureSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lines)
}

func (s *captureSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}

func TestStartLogCollector_CollectsLinesFromFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "proc.log")

	if err := os.WriteFile(logFile, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &captureSink{}
	stop := StartLogCollector(logFile, "nb:test", "runtime", sink)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sink.count() >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()

	got := sink.snapshot()
	if len(got) < 3 {
		t.Fatalf("collected %d lines, want 3; got %v", len(got), got)
	}
	for i, want := range []string{"alpha", "beta", "gamma"} {
		if got[i] != want {
			t.Fatalf("line[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestStartLogCollector_AppendedLinesAreCollected(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "append.log")

	f, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	sink := &captureSink{}
	stop := StartLogCollector(logFile, "nb:append", "runtime", sink)
	defer stop()

	// Write lines after the collector has started.
	if _, err := f.WriteString("first\nsecond\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Sync()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sink.count() >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got := sink.snapshot()
	if len(got) < 2 {
		t.Fatalf("collected %d lines, want ≥2; got %v", len(got), got)
	}
}

func TestStartLogCollector_MissingFileDoesNotPanic(t *testing.T) {
	sink := &captureSink{}
	// File does not exist yet — collector should either fail gracefully or poll.
	stop := StartLogCollector("/nonexistent/path/to/file.log", "nb:x", "runtime", sink)
	stop()
}
