package logsink

import (
	"testing"
	"time"
)

type redactingCaptureSink struct {
	line  string
	stops int
}

func (s *redactingCaptureSink) Append(_, _, _, line string, _ time.Time) {
	s.line = line
}

func (s *redactingCaptureSink) Stop() {
	s.stops++
}

func TestRedactingSink(t *testing.T) {
	capture := &redactingCaptureSink{}
	sink := NewRedactingSink(capture, []string{"super-secret-value"})

	sink.Append("run", "step", "stdout", "value=super-secret-value", time.Now())
	sink.Stop()

	if capture.line != "value=[REDACTED]" {
		t.Fatalf("line = %q", capture.line)
	}
	if capture.stops != 1 {
		t.Fatalf("stops = %d", capture.stops)
	}
}

func TestValuesFromEnv(t *testing.T) {
	got := ValuesFromEnv([]string{"A=one", "NO_VALUE", "B=two=still-value"})
	if len(got) != 2 || got[0] != "one" || got[1] != "two=still-value" {
		t.Fatalf("ValuesFromEnv() = %#v", got)
	}
}
