package commands

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/internal/testutil"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
)

func TestAgentExecReportsToDummyMaster(t *testing.T) {
	var doneTaskID string
	var sawLog bool
	master := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/done"):
			parts := strings.Split(r.URL.Path, "/")
			doneTaskID = parts[len(parts)-2]
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/logs"):
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read logs body: %v", err)
			}
			if strings.Contains(string(body), "hello from agent") {
				sawLog = true
			}
		}
		w.WriteHeader(http.StatusOK)
	}))

	step := pipeline.Step{
		Name: "agent-step",
		Run:  pipeline.Run{Command: []string{"echo", "hello from agent"}},
	}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	task := &proto.Task{
		ID:       "run-agent:agent-step",
		RunID:    "run-agent",
		StepName: "agent-step",
		Step:     stepJSON,
	}
	taskB64, err := agent.EncodeTask(task)
	if err != nil {
		t.Fatal(err)
	}

	if err := runAgentExec(context.Background(), agentExecFlags{
		master:    master.URL,
		taskB64:   taskB64,
		outputDir: t.TempDir(),
		inputDir:  t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}

	if doneTaskID != task.ID {
		t.Fatalf("done task id = %q, want %q", doneTaskID, task.ID)
	}
	if !sawLog {
		t.Fatal("dummy master did not receive agent logs")
	}
}
