package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/worker/agent"
)

func makeTask(t *testing.T, step pipeline.Step) *proto.Task {
	return makeTaskWithRunID(t, step, "run-test")
}

func makeTaskWithRunID(t *testing.T, step pipeline.Step, runID string) *proto.Task {
	t.Helper()
	b, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	return &proto.Task{ProjectID: "project-a", ID: runID + ":" + step.Name, RunID: runID, StepName: step.Name, Step: b}
}

func TestNew_defaults(t *testing.T) {
	r, err := agent.New(agent.Config{})
	if err != nil || r == nil {
		t.Fatalf("New() = %v, %v", r, err)
	}
}

func TestRun_successReturnsResultWithoutMasterConnection(t *testing.T) {
	r, err := agent.New(agent.Config{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	result := r.Run(context.Background(), makeTask(t, pipeline.Step{Name: "echo", Run: pipeline.Run{Command: []string{"echo", "ok"}}}))
	if result.Status != proto.TaskStatusDone || result.ProjectID != "project-a" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRun_createsOutputDirectory(t *testing.T) {
	out := t.TempDir()
	r, _ := agent.New(agent.Config{OutputDir: out})
	r.Run(context.Background(), makeTask(t, pipeline.Step{Name: "mkdir", Run: pipeline.Run{Command: []string{"echo", "ok"}}}))
	if _, err := os.Stat(filepath.Join(out, "run-test", "mkdir")); err != nil {
		t.Fatal(err)
	}
}

func TestRun_failureReturnedLocally(t *testing.T) {
	r, _ := agent.New(agent.Config{OutputDir: t.TempDir()})
	result := r.Run(context.Background(), makeTask(t, pipeline.Step{Name: "fail", Run: pipeline.Run{Command: []string{"__missing_command__"}}}))
	if result.Status != proto.TaskStatusFailed {
		t.Fatalf("status = %q", result.Status)
	}
}

func TestRun_includesFinalMetricsInResult(t *testing.T) {
	r, _ := agent.New(agent.Config{OutputDir: t.TempDir()})
	task := makeTask(t, pipeline.Step{Name: "train", Run: pipeline.Run{Command: []string{"sh", "-c", `echo '{"accuracy":0.94}' > "$PIPER_OUTPUT_DIR/.metrics.json"`}}})
	result := r.Run(context.Background(), task)
	if got := result.Metrics["accuracy"]; got != 0.94 {
		t.Fatalf("accuracy = %v", got)
	}
}

func TestRun_pythonStepPrependsVenvAndCleansUp(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	out := t.TempDir()
	r, err := agent.New(agent.Config{OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	step := pipeline.Step{
		Name: "pyenv",
		Run: pipeline.Run{
			Prepare: [][]string{
				{"sh", "-c", `dirname "$(command -v python)" > "$PIPER_OUTPUT_DIR/prepare-bin.txt"`},
			},
			Command: []string{"sh", "-c", `printf "%s|%s" "$PIPER_PYTHON_BIN" "$(dirname "$(command -v python)")" > "$PIPER_OUTPUT_DIR/runtime-bin.txt"`},
		},
	}

	result := r.Run(context.Background(), makeTask(t, step))
	if result.Status != proto.TaskStatusDone {
		t.Fatalf("status = %q, error = %s", result.Status, result.Error)
	}

	stepOut := filepath.Join(out, "run-test", "pyenv")
	wantBin := filepath.Join(stepOut, ".task-venv", "bin")
	prepareBin := strings.TrimSpace(readFile(t, filepath.Join(stepOut, "prepare-bin.txt")))
	if prepareBin != wantBin {
		t.Fatalf("prepare python bin = %q, want %q", prepareBin, wantBin)
	}
	runtimeParts := strings.Split(strings.TrimSpace(readFile(t, filepath.Join(stepOut, "runtime-bin.txt"))), "|")
	if len(runtimeParts) != 2 {
		t.Fatalf("runtime-bin format = %q", runtimeParts)
	}
	if runtimeParts[0] != filepath.Join(wantBin, "python") || runtimeParts[1] != wantBin {
		t.Fatalf("runtime python = %q, want python=%q bin=%q", runtimeParts, filepath.Join(wantBin, "python"), wantBin)
	}
	if _, err := os.Stat(filepath.Join(stepOut, ".task-venv")); !os.IsNotExist(err) {
		t.Fatalf("task venv was not cleaned up: %v", err)
	}
}

func TestRun_plainCommandSkipsVenv(t *testing.T) {
	out := t.TempDir()
	r, err := agent.New(agent.Config{OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	step := pipeline.Step{
		Name: "shell",
		Run: pipeline.Run{
			Command: []string{"sh", "-c", `printf "%s" "${PIPER_PYTHON_BIN:-}" > "$PIPER_OUTPUT_DIR/python-bin.txt"`},
		},
	}

	result := r.Run(context.Background(), makeTask(t, step))
	if result.Status != proto.TaskStatusDone {
		t.Fatalf("status = %q, error = %s", result.Status, result.Error)
	}

	stepOut := filepath.Join(out, "run-test", "shell")
	if got := readFile(t, filepath.Join(stepOut, "python-bin.txt")); got != "" {
		t.Fatalf("plain command PIPER_PYTHON_BIN = %q, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(stepOut, ".task-venv")); !os.IsNotExist(err) {
		t.Fatalf("plain command should not create task venv: %v", err)
	}
}

func TestRun_failedStepOmitsFinalMetrics(t *testing.T) {
	r, _ := agent.New(agent.Config{OutputDir: t.TempDir()})
	task := makeTask(t, pipeline.Step{Name: "fail", Run: pipeline.Run{Command: []string{"sh", "-c", `echo '{"x":1}' > "$PIPER_OUTPUT_DIR/.metrics.json"; exit 1`}}})
	result := r.Run(context.Background(), task)
	if len(result.Metrics) != 0 {
		t.Fatalf("failed result metrics = %v", result.Metrics)
	}
}

// TestRun_commandCwdMatchesWorkspaceOutputDir guards against regressing to a
// bug where a Command step without an explicit source (task.WorkDir) ran with
// cwd "." — the agent process's own launch directory — instead of its
// isolated per-run/per-step workspace. A relative "outputs:" path is only
// found by uploadOutputs when the step's command actually wrote it into that
// same workspace directory.
func TestRun_commandCwdMatchesWorkspaceOutputDir(t *testing.T) {
	out := t.TempDir()
	store := t.TempDir()
	r, err := agent.New(agent.Config{OutputDir: out, StorageURL: "file://" + store})
	if err != nil {
		t.Fatal(err)
	}

	step := pipeline.Step{
		Name: "task-1",
		Run:  pipeline.Run{Command: []string{"sh", "-c", "echo model-marker > model.txt"}},
		Outputs: []pipeline.Artifact{
			{Name: "model", Path: "model.txt"},
		},
	}
	task := makeTaskWithRunID(t, step, "run-workdir")
	task.WorkDir = "." // what piper.go still sends; must not be trusted as-is

	result := r.Run(context.Background(), task)
	if result.Status != proto.TaskStatusDone {
		t.Fatalf("run failed: %+v", result)
	}

	uploaded := filepath.Join(store, "run-workdir", "task-1", "model", "model.txt")
	got := readFile(t, uploaded)
	if got != "model-marker\n" {
		t.Fatalf("artifact content = %q", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
