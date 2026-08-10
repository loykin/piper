package piper

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/logstore"
)

type recordingRunLiveness struct {
	mu       sync.Mutex
	workerID string
	runIDs   []string
	calls    int
}

func (r *recordingRunLiveness) TouchWorkerLastSeen(_ context.Context, workerID string, runIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workerID = workerID
	r.runIDs = runIDs
	r.calls++
	return nil
}

type recordingLogStore struct {
	mu    sync.Mutex
	lines []*logstore.Line
}

func (s *recordingLogStore) Append(_ context.Context, lines []*logstore.Line) error {
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
	handler := newWorkerPushHandler(nil, nil, nil, store, nil)

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
	handler := newWorkerPushHandler(nil, nil, nil, nil, nil)

	payload, _ := json.Marshal(logsink.LogAppendPush{
		ProjectID: "proj-1", RunID: "nb:test", StepName: "runtime",
		Lines: []logsink.LogLine{{Stream: "stdout", Text: "x", Ts: time.Now()}},
	})
	// Must not panic.
	handler(context.Background(), "worker-1", iagent.MethodLogAppend, payload)
}

func TestWorkerPushLeaseRenewTouchesRunLiveness(t *testing.T) {
	liveness := &recordingRunLiveness{}
	handler := newWorkerPushHandler(nil, nil, liveness, nil, nil)

	payload, _ := json.Marshal(map[string]any{
		"run_ids": []string{"run-1", "run-2"},
	})
	handler(context.Background(), "worker-1", iagent.MethodPipelineLeaseRenew, payload)

	if liveness.calls != 1 {
		t.Fatalf("TouchWorkerLastSeen called %d times, want 1", liveness.calls)
	}
	if liveness.workerID != "worker-1" {
		t.Fatalf("workerID = %q, want worker-1", liveness.workerID)
	}
	if len(liveness.runIDs) != 2 || liveness.runIDs[0] != "run-1" || liveness.runIDs[1] != "run-2" {
		t.Fatalf("runIDs = %#v, want [run-1 run-2]", liveness.runIDs)
	}
}

func TestWorkerPushLeaseRenewSkipsRunLivenessWhenNoRunIDs(t *testing.T) {
	liveness := &recordingRunLiveness{}
	handler := newWorkerPushHandler(nil, nil, liveness, nil, nil)

	payload, _ := json.Marshal(map[string]any{})
	handler(context.Background(), "worker-1", iagent.MethodPipelineLeaseRenew, payload)

	if liveness.calls != 0 {
		t.Fatalf("TouchWorkerLastSeen called %d times for a push with no run_ids, want 0", liveness.calls)
	}
}
