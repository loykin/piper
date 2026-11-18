package localdriver

import (
	"strings"
	"sync"
	"time"

	"github.com/loykin/piper/internal/logsink"
)

// tailSink remembers the last non-empty line a service wrote, so a service
// that exits on its own can report why ("command not found", a Python
// traceback's last line) instead of a bare "failed". It sits behind the
// redacting sink, so the remembered line is already redacted.
type tailSink struct {
	next logsink.LogSink
	mu   sync.Mutex
	last string
}

func newTailSink(next logsink.LogSink) *tailSink { return &tailSink{next: next} }

func (s *tailSink) Append(runID, stepName, stream, line string, ts time.Time) {
	if trimmed := strings.TrimSpace(line); trimmed != "" {
		s.mu.Lock()
		s.last = trimmed
		s.mu.Unlock()
	}
	if s.next != nil {
		s.next.Append(runID, stepName, stream, line, ts)
	}
}

func (s *tailSink) Stop() {
	if s.next != nil {
		s.next.Stop()
	}
}

func (s *tailSink) lastLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}
