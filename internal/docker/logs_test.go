package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	dockerclient "github.com/moby/moby/client"
)

type logClient struct {
	API
	logs dockerclient.ContainerLogsResult
	err  error
}

func (c *logClient) ContainerLogs(context.Context, string, dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
	return c.logs, c.err
}

type capturedLine struct{ runID, stepName, stream, line string }

type captureSink struct {
	mu        sync.Mutex
	lines     []capturedLine
	stopCount int
}

func (s *captureSink) Append(runID, stepName, stream, line string, _ time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, capturedLine{runID, stepName, stream, line})
}

func (s *captureSink) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCount++
}

func TestStreamLogsDemultiplexesAndFlushesPartialLine(t *testing.T) {
	var stream bytes.Buffer
	writeFrame(&stream, stdcopy.Stdout, []byte("out one\nout two"))
	writeFrame(&stream, stdcopy.Stderr, []byte("err one\n"))
	sink := &captureSink{}
	StreamLogs(&logClient{logs: io.NopCloser(&stream)}, "container-1", "run-1", "runtime", sink)

	if sink.stopCount != 1 {
		t.Fatalf("Stop called %d times, want 1", sink.stopCount)
	}
	want := []capturedLine{
		{"run-1", "runtime", "stdout", "out one"},
		{"run-1", "runtime", "stderr", "err one"},
		{"run-1", "runtime", "stdout", "out two"},
	}
	if len(sink.lines) != len(want) {
		t.Fatalf("lines = %#v, want %#v", sink.lines, want)
	}
	for i := range want {
		if sink.lines[i] != want[i] {
			t.Fatalf("line[%d] = %#v, want %#v", i, sink.lines[i], want[i])
		}
	}
}

func TestStreamLogsHandlesLongLine(t *testing.T) {
	longLine := strings.Repeat("x", 256*1024)
	var stream bytes.Buffer
	writeFrame(&stream, stdcopy.Stdout, []byte(longLine+"\n"))
	sink := &captureSink{}
	StreamLogs(&logClient{logs: io.NopCloser(&stream)}, "container-1", "run-1", "runtime", sink)

	if len(sink.lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(sink.lines))
	}
	if got := len(sink.lines[0].line); got != len(longLine) {
		t.Fatalf("long line length = %d, want %d", got, len(longLine))
	}
}

func TestStreamLogsStopsSinkWhenLogsFail(t *testing.T) {
	sink := &captureSink{}
	StreamLogs(&logClient{err: errors.New("unavailable")}, "container-1", "run-1", "runtime", sink)
	if sink.stopCount != 1 {
		t.Fatalf("Stop called %d times, want 1", sink.stopCount)
	}
}

func writeFrame(dst *bytes.Buffer, stream stdcopy.StdType, payload []byte) {
	header := make([]byte, 8)
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	dst.Write(header)
	dst.Write(payload)
}
