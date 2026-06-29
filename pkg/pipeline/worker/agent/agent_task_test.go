package agent_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
)

func TestTaskCodecPreservesContract(t *testing.T) {
	scheduledAt := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	step := pipeline.Step{
		Name:   "train",
		Run:    pipeline.Run{Command: []string{"echo", "train"}},
		Params: map[string]any{"lr": "0.1"},
	}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	original := &proto.Task{
		ID:        "run-1:train",
		RunID:     "run-1",
		StepName:  "train",
		Step:      stepJSON,
		Pipeline:  []byte(`{"metadata":{"name":"pipe"}}`),
		WorkDir:   "/work",
		OutputDir: "/out",
		Attempt:   2,
		Vars:      proto.BuiltinVars{ScheduledAt: &scheduledAt},
		RunParams: map[string]any{"lr": "0.2"},
		Env:       []string{"PIPER_GIT_TOKEN=tok", "PIPER_GIT_USER=user"},
	}
	encoded, err := agent.EncodeTask(original)
	if err != nil {
		t.Fatal(err)
	}

	task, err := agent.DecodeTask(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(task, original) {
		t.Fatalf("task contract not preserved: %#v", task)
	}
}

func TestDecodeTaskRejectsInvalidInput(t *testing.T) {
	if _, err := agent.DecodeTask("not-base64"); err == nil {
		t.Fatal("expected invalid task encoding to fail")
	}
}
