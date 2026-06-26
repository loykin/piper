package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
)

func TestRunAgentExecWritesResultWithIsolatedPython(t *testing.T) {
	step := pipeline.Step{
		Name: "hello",
		Run:  pipeline.Run{Command: []string{"sh", "-c", "echo ok"}},
	}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	taskB64, err := agent.EncodeTask(&proto.Task{
		ProjectID: "e2e",
		ID:        "run-test:hello",
		RunID:     "run-test",
		StepName:  "hello",
		Step:      stepJSON,
	})
	if err != nil {
		t.Fatal(err)
	}

	resultFile := filepath.Join(t.TempDir(), "result.json")
	code := runAgentExec([]string{
		"--task=" + taskB64,
		"--result-file=" + resultFile,
		"--output-dir=" + t.TempDir(),
		"--isolated-python",
	})
	if code != 0 {
		t.Fatalf("runAgentExec() exit = %d", code)
	}
	if _, err := os.Stat(resultFile); err != nil {
		t.Fatalf("result file not written: %v", err)
	}
}
