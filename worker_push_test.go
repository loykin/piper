package piper

import (
	"context"
	"encoding/json"
	"testing"

	iagent "github.com/piper/piper/internal/agent"
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

func TestWorkerPushAcknowledgesCompletedPipelineResult(t *testing.T) {
	queue := &recordingPipelineStatusQueue{}
	acker := &recordingPipelineResultAcker{}
	handler := newWorkerPushHandler(nil, nil, queue, acker)
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
