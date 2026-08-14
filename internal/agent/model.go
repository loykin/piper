// Package agent holds small pieces of state shared between the in-process
// direct-runtime backends (internal/pipelinedispatch, internal/directworker,
// and the notebook/serving localdriver packages): a capacity-refusal error
// type and the log-push method name used by internal/logsink's PushClient.
package agent

// BusyErrorMarker is embedded in the error message so a caller can identify
// a capacity refusal even after the error crosses a process boundary and is
// serialized to a plain string.
const BusyErrorMarker = "[piper:worker_busy]"

// BusyError is returned when a direct-runtime backend cannot accept a
// dispatch because it is at capacity. The caller should treat this as a
// retryable dispatch failure and not count it as a step retry.
type BusyError struct {
	Reason string
}

func (e *BusyError) Error() string {
	if e.Reason != "" {
		return BusyErrorMarker + " " + e.Reason
	}
	return BusyErrorMarker
}

// MethodLogAppend tags a logsink.PushClient.SendPush call as a batch of log
// lines. Used in-process by internal/logsink's BufferedLogSink
// (despite the name, no gRPC is involved once dispatch is direct-runtime —
// see piper.go's localLogPushClient).
const MethodLogAppend = "log.append"
