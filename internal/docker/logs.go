package docker

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	dockerclient "github.com/moby/moby/client"

	"github.com/piper/piper/internal/logsink"
)

// StreamLogs demultiplexes a Docker log stream and forwards complete lines to
// sink. It owns the sink and stops it before returning.
func StreamLogs(cli API, containerID, runID, stepName string, sink logsink.LogSink) {
	defer sink.Stop()
	rc, err := cli.ContainerLogs(context.Background(), containerID, dockerclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		slog.Warn("docker logs: cannot open stream", "container_id", containerID, "err", err)
		return
	}
	defer func() { _ = rc.Close() }()

	stdout := newLineWriter(runID, stepName, "stdout", sink)
	stderr := newLineWriter(runID, stepName, "stderr", sink)
	if _, err := stdcopy.StdCopy(stdout, stderr, rc); err != nil {
		slog.Warn("docker logs: stream copy failed", "container_id", containerID, "err", err)
	}
	stdout.Close()
	stderr.Close()
}

type lineWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	runID    string
	stepName string
	stream   string
	sink     logsink.LogSink
	closed   bool
}

func newLineWriter(runID, stepName, stream string, sink logsink.LogSink) *lineWriter {
	return &lineWriter{runID: runID, stepName: stepName, stream: stream, sink: sink}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	w.buf.Write(p)
	w.flushLines(false)
	return len(p), nil
}

func (w *lineWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	w.flushLines(true)
}

func (w *lineWriter) flushLines(flushPartial bool) {
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			if flushPartial && len(data) > 0 {
				w.appendLine(string(data))
				w.buf.Reset()
			}
			return
		}
		line := data[:idx]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		w.appendLine(string(line))
		w.buf.Next(idx + 1)
	}
}

func (w *lineWriter) appendLine(line string) {
	w.sink.Append(w.runID, w.stepName, w.stream, line, time.Now())
}
