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

func TestBuildAgentExecIncludesGitCredentialFlags(t *testing.T) {
	args, err := BuildAgentExec(&proto.Task{ID: "run:step"}, AgentExecConfig{
		ResultFile: "/tmp/result.json",
		GitUser:    "git-user",
		GitToken:   "git-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--git-user=git-user") || !strings.Contains(joined, "--git-token=git-token") {
		t.Fatalf("child args missing git credential flags: %s", joined)
	}
}
