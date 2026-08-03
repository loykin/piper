package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/worker/agent"
)

func TestRunAgentExecWritesResult(t *testing.T) {
	step := pipeline.Step{
		Name: "hello",
		Run:  pipeline.Run{Command: []string{"sh", "-c", "echo ok"}},
	}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	task := &proto.Task{
		ProjectID: "e2e",
		ID:        "run-test:hello",
		RunID:     "run-test",
		StepName:  "hello",
		Step:      stepJSON,
	}
	taskFile := filepath.Join(t.TempDir(), "task.json")
	if err := agent.WriteTaskFile(taskFile, task); err != nil {
		t.Fatal(err)
	}

	resultFile := filepath.Join(t.TempDir(), "result.json")
	code := runAgentExec([]string{
		"--task-file=" + taskFile,
		"--result-file=" + resultFile,
		"--output-dir=" + t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("runAgentExec() exit = %d", code)
	}
	if _, err := os.Stat(resultFile); err != nil {
		t.Fatalf("result file not written: %v", err)
	}
}
