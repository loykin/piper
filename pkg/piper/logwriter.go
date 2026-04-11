package piper

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"time"

	"github.com/piper/piper/pkg/logstore"
)

// storeLogWriter implements io.Writer,
// tee-ing to the terminal while persisting each line to the LogStore.
type storeLogWriter struct {
	logs     logstore.LogStore
	runID    string
	stepName string
	stream   string
	tee      io.Writer
}

func (w *storeLogWriter) Write(p []byte) (int, error) {
	if w.tee != nil {
		_, _ = w.tee.Write(p)
	}

	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		line := scanner.Text()
		if err := w.logs.Append([]*logstore.Line{{
			RunID:    w.runID,
			StepName: w.stepName,
			Ts:       time.Now(),
			Stream:   w.stream,
			Line:     line,
		}}); err != nil {
			slog.Warn("log store failed", "err", err)
		}
	}
	return len(p), nil
}
