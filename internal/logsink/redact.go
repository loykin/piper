package logsink

import (
	"strings"
	"time"

	"github.com/piper/piper/internal/redact"
)

type redactingSink struct {
	next   LogSink
	values []string
}

// NewRedactingSink masks known sensitive values before forwarding log lines.
func NewRedactingSink(next LogSink, values []string) LogSink {
	if next == nil || len(values) == 0 {
		return next
	}
	return &redactingSink{next: next, values: values}
}

func (s *redactingSink) Append(runID, stepName, stream, line string, ts time.Time) {
	s.next.Append(runID, stepName, stream, redact.Values(line, s.values), ts)
}

func (s *redactingSink) Stop() {
	s.next.Stop()
}

// ValuesFromEnv extracts values from KEY=value entries for log redaction.
func ValuesFromEnv(env []string) []string {
	values := make([]string, 0, len(env))
	for _, entry := range env {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			values = append(values, entry[idx+1:])
		}
	}
	return values
}
