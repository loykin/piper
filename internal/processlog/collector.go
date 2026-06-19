// Package processlog streams process log files into a LogSink.
package processlog

import (
	"log/slog"
	"sync"
	"time"

	"github.com/loykin/freader"

	"github.com/piper/piper/internal/logsink"
)

// StartCollector watches logFile and forwards lines to sink. The returned stop
// function is safe to call more than once.
func StartCollector(logFile, runID, stepName string, sink logsink.LogSink) func() {
	cfg := freader.Config{
		WorkerCount:         1,
		PollInterval:        200 * time.Millisecond,
		Separator:           "\n",
		FingerprintStrategy: freader.FingerprintStrategyDeviceAndInode,
		Include:             []string{logFile},
		StoreOffsets:        false,
		OnEventFunc: func(e freader.LineEvent) {
			sink.Append(runID, stepName, "combined", e.Line, e.Ts)
		},
	}
	collector, err := freader.NewCollector(cfg)
	if err != nil {
		slog.Warn("process log collector: cannot create", "log_file", logFile, "err", err)
		return func() {}
	}
	collector.Start()
	var once sync.Once
	return func() { once.Do(collector.Stop) }
}
