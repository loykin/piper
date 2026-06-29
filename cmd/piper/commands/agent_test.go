package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/piper/piper/pkg/pipeline/worker/driver"
)

func TestAgentExecWritesResultForParentWorker(t *testing.T) {
	stepJSON, err := json.Marshal(pipeline.Step{Name: "agent-step", Run: pipeline.Run{Command: []string{"echo", "hello from agent"}}})
	if err != nil {
		t.Fatal(err)
	}
	task := &proto.Task{ProjectID: "project-a", ID: "run-agent:agent-step", RunID: "run-agent", StepName: "agent-step", Step: stepJSON}
	taskB64, err := agent.EncodeTask(task)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.json")
	if err := runAgentExec(context.Background(), agentExecFlags{
		taskB64: taskB64, outputDir: dir, inputDir: dir, resultFile: resultFile,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.ReadAgentResult(data)
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != task.ID || result.Status != proto.TaskStatusDone {
		t.Fatalf("result = %+v", result)
	}
}

func TestDriverEnvValueReadsGitCredentialEnv(t *testing.T) {
	env := []string{"OTHER=value", "PIPER_GIT_USER=git-user", "PIPER_GIT_TOKEN=git-token"}
	if got := pdriver.EnvValue(env, "PIPER_GIT_USER"); got != "git-user" {
		t.Fatalf("PIPER_GIT_USER = %q", got)
	}
	if got := pdriver.EnvValue(env, "PIPER_GIT_TOKEN"); got != "git-token" {
		t.Fatalf("PIPER_GIT_TOKEN = %q", got)
	}
	if got := pdriver.EnvValue(env, "MISSING"); got != "" {
		t.Fatalf("MISSING = %q", got)
	}
}
