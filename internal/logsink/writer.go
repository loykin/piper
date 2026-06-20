package logsink

import (
	"bytes"
	"sync"
	"time"
)

// NewLineWriter converts a byte stream into timestamped LogSink lines.
func NewLineWriter(sink LogSink, runID, stepName, stream string) *LineWriter {
	return &LineWriter{sink: sink, runID: runID, stepName: stepName, stream: stream}
}

type LineWriter struct {
	mu              sync.Mutex
	buf             bytes.Buffer
	sink            LogSink
	runID, stepName string
	stream          string
}

func (w *LineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	_, _ = w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			w.buf.WriteString(line)
			break
		}
		w.sink.Append(w.runID, w.stepName, w.stream, string(bytes.TrimRight([]byte(line), "\r\n")), time.Now())
	}
	// Process pipes do not provide a close callback for injected writers. Flush
	// a trailing fragment now so a final line without a newline is never lost.
	if w.buf.Len() > 0 {
		w.sink.Append(w.runID, w.stepName, w.stream, w.buf.String(), time.Now())
		w.buf.Reset()
	}
	return n, nil
}

func (w *LineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.sink.Append(w.runID, w.stepName, w.stream, w.buf.String(), time.Now())
		w.buf.Reset()
	}
}
