package piper

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline/worker/driver"
)

type recordingPipelineStatusQueue struct {
	result proto.TaskResult
}

func (q *recordingPipelineStatusQueue) Complete(_ context.Context, result proto.TaskResult) error {
	q.result = result
	return nil
}

func (q *recordingPipelineStatusQueue) RenewLeases(string, []string) {}

type recordingPipelineResultAcker struct {
	agentID string
	method  string
	ack     driver.ResultAck
}

func (a *recordingPipelineResultAcker) SendRPC(_ context.Context, agentID, method string, payload any, _ any) error {
	a.agentID = agentID
	a.method = method
	a.ack = payload.(driver.ResultAck)
	return nil
}

type recordingLogStore struct {
	mu    sync.Mutex
	lines []*logstore.Line
}

type recordingMetricStore struct{ metrics []*logstore.Metric }

func (s *recordingMetricStore) AppendMetrics(metrics []*logstore.Metric) error {
	s.metrics = append(s.metrics, metrics...)
	return nil
}
func (s *recordingMetricStore) QueryMetrics(_, _, _ string) ([]*logstore.Metric, error) {
	return s.metrics, nil
}

func (s *recordingLogStore) Append(lines []*logstore.Line) error {
	s.mu.Lock()
	s.lines = append(s.lines, lines...)
	s.mu.Unlock()
	return nil
}

func (s *recordingLogStore) Query(_, _, _ string, _ int64) ([]*logstore.Line, error) {
	return nil, nil
}

func (s *recordingLogStore) all() []*logstore.Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*logstore.Line, len(s.lines))
	copy(out, s.lines)
	return out
}

func TestWorkerPushHandlerLogAppend(t *testing.T) {
	store := &recordingLogStore{}
	handler := newWorkerPushHandler(nil, nil, nil, nil, store, nil)

	now := time.Now().UTC().Truncate(time.Millisecond)
	push := logsink.LogAppendPush{
		ProjectID: "proj-1",
		RunID:     "nb:mynotebook",
		StepName:  "runtime",
		Lines: []logsink.LogLine{
			{Stream: "stdout", Text: "hello", Ts: now},
			{Stream: "stdout", Text: "world", Ts: now.Add(time.Second)},
		},
	}
	payload, err := json.Marshal(push)
	if err != nil {
		t.Fatal(err)
	}

	handler(context.Background(), "worker-1", iagent.MethodLogAppend, payload)

	got := store.all()
	if len(got) != 2 {
		t.Fatalf("stored %d lines, want 2", len(got))
	}
	for i, want := range []string{"hello", "world"} {
		if got[i].Line != want {
			t.Fatalf("line[%d].Line = %q, want %q", i, got[i].Line, want)
		}
		if got[i].ProjectID != "proj-1" || got[i].RunID != "nb:mynotebook" || got[i].StepName != "runtime" {
			t.Fatalf("line[%d] metadata = %+v", i, got[i])
		}
	}
}

func TestWorkerPushHandlerLogAppend_NilStoreDropsSilently(t *testing.T) {
	handler := newWorkerPushHandler(nil, nil, nil, nil, nil, nil)

	payload, _ := json.Marshal(logsink.LogAppendPush{
		ProjectID: "proj-1", RunID: "nb:test", StepName: "runtime",
		Lines: []logsink.LogLine{{Stream: "stdout", Text: "x", Ts: time.Now()}},
	})
	// Must not panic.
	handler(context.Background(), "worker-1", iagent.MethodLogAppend, payload)
}

func TestWorkerPushAcknowledgesCompletedPipelineResult(t *testing.T) {
	queue := &recordingPipelineStatusQueue{}
	acker := &recordingPipelineResultAcker{}
	handler := newWorkerPushHandler(nil, nil, queue, acker, nil, nil)
	result := proto.TaskResult{
		TaskID:  "run-1:step-1",
		Attempt: 2,
		Status:  proto.TaskStatusDone,
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	handler(context.Background(), "worker-1", iagent.MethodPipelineTaskResult, payload)

	if queue.result.WorkerID != "worker-1" {
		t.Fatalf("queue worker ID = %q, want worker-1", queue.result.WorkerID)
	}
	if acker.agentID != "worker-1" || acker.method != iagent.MethodPipelineResultAck {
		t.Fatalf("ack target = (%q, %q)", acker.agentID, acker.method)
	}
	if acker.ack.TaskID != result.TaskID || acker.ack.Attempt != result.Attempt {
		t.Fatalf("ack = %#v, want task %q attempt %d", acker.ack, result.TaskID, result.Attempt)
	}
}

func TestWorkerPushPersistsFinalMetrics(t *testing.T) {
	queue := &recordingPipelineStatusQueue{}
	metrics := &recordingMetricStore{}
	handler := newWorkerPushHandler(nil, nil, queue, nil, nil, metrics)
	payload, _ := json.Marshal(proto.TaskResult{
		ProjectID: "proj-1",
		TaskID:    "run-1:train",
		Status:    proto.TaskStatusDone,
		Metrics:   map[string]float64{"accuracy": 0.94},
	})

	handler(context.Background(), "worker-1", iagent.MethodPipelineTaskResult, payload)

	if len(metrics.metrics) != 1 {
		t.Fatalf("stored metrics = %#v", metrics.metrics)
	}
	got := metrics.metrics[0]
	if got.ProjectID != "proj-1" || got.RunID != "run-1" || got.StepName != "train" || got.Key != "accuracy" || got.Value != 0.94 {
		t.Fatalf("metric = %+v", got)
	}
}
