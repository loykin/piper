package runner_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
)

func TestTaskFromAgentInputFullTaskPreservesContract(t *testing.T) {
	scheduledAt := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	step := pipeline.Step{
		Name:   "train",
		Run:    pipeline.Run{Command: []string{"echo", "old"}},
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
	}
	encoded, err := runner.EncodeTask(original)
	if err != nil {
		t.Fatal(err)
	}

	task, err := runner.TaskFromAgentInput(encoded, "", "", "", "", []string{"echo", "new"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != original.ID || task.WorkDir != original.WorkDir || task.Attempt != original.Attempt {
		t.Fatalf("task contract not preserved: %#v", task)
	}
	if task.Vars.ScheduledAt == nil || !task.Vars.ScheduledAt.Equal(scheduledAt) {
		t.Fatalf("scheduled_at = %v, want %v", task.Vars.ScheduledAt, scheduledAt)
	}
	if task.RunParams["lr"] != "0.2" {
		t.Fatalf("RunParams = %#v, want lr override", task.RunParams)
	}
	var decodedStep pipeline.Step
	if err := json.Unmarshal(task.Step, &decodedStep); err != nil {
		t.Fatal(err)
	}
	if got := decodedStep.Run.Command; len(got) != 2 || got[1] != "new" {
		t.Fatalf("command override = %#v, want [echo new]", got)
	}
}

func TestTaskFromAgentInputLegacyStep(t *testing.T) {
	step := pipeline.Step{Name: "legacy", Run: pipeline.Run{Command: []string{"echo", "legacy"}}}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	stepB64 := base64.StdEncoding.EncodeToString(stepJSON)

	task, err := runner.TaskFromAgentInput("", "task-1", "run-1", "legacy", stepB64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "task-1" || task.RunID != "run-1" || task.StepName != "legacy" {
		t.Fatalf("legacy task = %#v", task)
	}
}
