// Package logsink provides the LogSink interface and gRPC-backed implementation
// for streaming baremetal process logs to the master logstore.
package logsink

import (
	"log/slog"
	"sync"
	"time"

	iagent "github.com/piper/piper/internal/agent"
)

// LogSink receives log lines collected from a running process and forwards them
// to the master logstore. Append is non-blocking; Stop flushes any buffered
// lines and waits for the background sender to exit.
type LogSink interface {
	Append(runID, stepName, stream, line string, ts time.Time)
	Stop()
}

// PushClient is the subset of grpcagent.Client used by GRPCLogSink.
type PushClient interface {
	SendPush(method string, payload any) error
}

// LogAppendPush is the gRPC push payload for MethodLogAppend.
type LogAppendPush struct {
	ProjectID string    `json:"project_id"`
	RunID     string    `json:"run_id"`
	StepName  string    `json:"step_name"`
	Lines     []LogLine `json:"lines"`
}

// LogLine is a single log entry within a LogAppendPush.
type LogLine struct {
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
	Ts     time.Time `json:"ts"`
}

type pendingLine struct {
	runID    string
	stepName string
	stream   string
	text     string
	ts       time.Time
}

// GRPCLogSink buffers log lines and forwards them to the master via gRPC push.
// Lines are flushed every 500 ms or when the internal buffer reaches 256 lines.
type GRPCLogSink struct {
	projectID string
	client    PushClient
	ch        chan pendingLine
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

const (
	logSinkBufSize       = 512
	logSinkBatchSize     = 256
	logSinkFlushInterval = 500 * time.Millisecond
)

// NewGRPCLogSink creates a GRPCLogSink and starts its background flush goroutine.
func NewGRPCLogSink(projectID string, client PushClient) *GRPCLogSink {
	s := &GRPCLogSink{
		projectID: projectID,
		client:    client,
		ch:        make(chan pendingLine, logSinkBufSize),
		stopCh:    make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

// Append enqueues a log line for delivery. Drops the line if the internal
// channel is full to avoid blocking the caller.
func (s *GRPCLogSink) Append(runID, stepName, stream, line string, ts time.Time) {
	select {
	case s.ch <- pendingLine{runID: runID, stepName: stepName, stream: stream, text: line, ts: ts}:
	default:
		slog.Debug("logsink: buffer full, dropping line", "run_id", runID)
	}
}

// Stop signals the background goroutine to flush remaining lines and waits
// for it to finish. Safe to call more than once.
func (s *GRPCLogSink) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *GRPCLogSink) run() {
	defer s.wg.Done()

	ticker := time.NewTicker(logSinkFlushInterval)
	defer ticker.Stop()

	var buf []pendingLine

	flush := func() {
		if len(buf) == 0 {
			return
		}
		s.send(buf)
		buf = buf[:0]
	}

	for {
		select {
		case line := <-s.ch:
			buf = append(buf, line)
			if len(buf) >= logSinkBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stopCh:
			// Drain remaining lines before exiting.
			for {
				select {
				case line := <-s.ch:
					buf = append(buf, line)
				default:
					flush()
					return
				}
			}
		}
	}
}

// send groups lines by (runID, stepName) and issues one push per group.
func (s *GRPCLogSink) send(lines []pendingLine) {
	type key struct{ runID, stepName string }
	groups := make(map[key][]LogLine, 2)
	for _, l := range lines {
		k := key{l.runID, l.stepName}
		groups[k] = append(groups[k], LogLine{Stream: l.stream, Text: l.text, Ts: l.ts})
	}
	for k, ll := range groups {
		payload := LogAppendPush{
			ProjectID: s.projectID,
			RunID:     k.runID,
			StepName:  k.stepName,
			Lines:     ll,
		}
		if err := s.client.SendPush(iagent.MethodLogAppend, payload); err != nil {
			slog.Debug("logsink: push failed", "run_id", k.runID, "err", err)
		}
	}
}
