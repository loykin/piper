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

func TestRunAgentExec(t *testing.T) {
	out := t.TempDir()
	step := pipeline.Step{
		Name: "hello",
		Run: pipeline.Run{
			Command: []string{"sh", "-c", `printf ok > "$PIPER_OUTPUT_DIR/result.txt"`},
		},
	}
	stepBytes, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	task := &proto.Task{
		ProjectID: "default",
		ID:        "run-test:hello",
		RunID:     "run-test",
		StepName:  "hello",
		Step:      stepBytes,
	}
	taskFile := filepath.Join(out, "task.json")
	if err := agent.WriteTaskFile(taskFile, task); err != nil {
		t.Fatal(err)
	}
	resultFile := filepath.Join(out, "result.json")

	runAgentExec([]string{
		"--task-file=" + taskFile,
		"--output-dir=" + out,
		"--input-dir=" + out,
		"--result-file=" + resultFile,
	})

	if _, err := os.Stat(resultFile); err != nil {
		t.Fatalf("result file was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "run-test", "hello", "result.txt")); err != nil {
		t.Fatalf("step output was not written: %v", err)
	}
}
