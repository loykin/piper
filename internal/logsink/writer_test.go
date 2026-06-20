package logsink

import (
	"testing"
	"time"
)

type captureSink struct{ lines []LogLine }

func (s *captureSink) Append(_, _, stream, line string, ts time.Time) {
	s.lines = append(s.lines, LogLine{Stream: stream, Text: line, Ts: ts})
}
func (*captureSink) Stop() {}

func TestLineWriterForwardsNewlineAndTrailingFragment(t *testing.T) {
	sink := &captureSink{}
	w := NewLineWriter(sink, "run-1", "step-1", "stdout")
	_, _ = w.Write([]byte("first\nlast"))
	if len(sink.lines) != 2 || sink.lines[0].Text != "first" || sink.lines[1].Text != "last" {
		t.Fatalf("lines = %#v", sink.lines)
	}
}
