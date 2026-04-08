package piper

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"time"

	"github.com/piper/piper/pkg/store"
)

// storeLogWriter는 io.Writer를 구현하며
// 터미널에 tee하면서 라인 단위로 store에 저장한다
type storeLogWriter struct {
	store    *store.Store
	runID    string
	stepName string
	stream   string
	tee      io.Writer
}

func (w *storeLogWriter) Write(p []byte) (int, error) {
	if w.tee != nil {
		w.tee.Write(p)
	}

	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		line := scanner.Text()
		if err := w.store.AppendLog(&store.LogLine{
			RunID:    w.runID,
			StepName: w.stepName,
			Ts:       time.Now(),
			Stream:   w.stream,
			Line:     line,
		}); err != nil {
			slog.Warn("log store failed", "err", err)
		}
	}
	return len(p), nil
}
