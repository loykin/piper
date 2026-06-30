package agent

import (
	"strings"
	"testing"

	"github.com/piper/piper/internal/proto"
)

func TestBuildAgentExecRequiresLocalResultFile(t *testing.T) {
	_, err := BuildAgentExec(&proto.Task{ID: "run:step"}, AgentExecConfig{})
	if err == nil || !strings.Contains(err.Error(), "ResultFile is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildAgentExecHasNoMasterCallbackArguments(t *testing.T) {
	args, err := BuildAgentExec(&proto.Task{ID: "run:step"}, AgentExecConfig{ResultFile: "/tmp/result.json"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--master") || strings.Contains(joined, "--worker-token") || strings.Contains(joined, "--report-mode") {
		t.Fatalf("child args contain legacy callback configuration: %s", joined)
	}
}

func TestBuildAgentExecHasNoGitCredentialFlags(t *testing.T) {
	args, err := BuildAgentExec(&proto.Task{
		ID:  "run:step",
		Env: []string{"PIPER_GIT_USER=git-user", "PIPER_GIT_TOKEN=git-token"},
	}, AgentExecConfig{ResultFile: "/tmp/result.json"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--git-user") || strings.Contains(joined, "--git-token") {
		t.Fatalf("child args must not expose git credentials as CLI flags: %s", joined)
	}
}
