// Package taskruntime provides the RuntimeDriver interface and shared
// utilities for executing pipeline steps across different environments.
package taskruntime

import (
	"encoding/json"
	"fmt"

	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
)

// ReportMode controls how piper agent exec delivers the TaskResult.
type ReportMode string

const (
	// ReportModeHTTP posts the result to the master HTTP endpoint.
	// Used during migration while drivers do not yet consume result files.
	ReportModeHTTP ReportMode = "http"

	// ReportModeFile writes the result to --result-file atomically.
	// Final target mode once all drivers read result files.
	ReportModeFile ReportMode = "file"
)

// AgentExecConfig holds the parameters needed to build a piper agent exec
// argument list. It is environment-agnostic; each RuntimeDriver supplies
// the paths it mounts or creates.
type AgentExecConfig struct {
	MasterURL  string
	Token      string
	StorageURL string
	OutputDir  string
	InputDir   string
	// ResultFile is the path inside the execution environment where the agent
	// writes the AgentResult JSON. Required when ReportMode is ReportModeFile.
	ResultFile string
	// ReportMode is required; zero value is rejected.
	ReportMode ReportMode
}

// BuildAgentExec returns the argv slice for `piper agent exec`, not including
// the binary itself. The task is base64-encoded and passed as --task.
// Returns an error if ReportMode is empty or the task cannot be encoded.
func BuildAgentExec(task *proto.Task, cfg AgentExecConfig) ([]string, error) {
	if cfg.ReportMode == "" {
		return nil, fmt.Errorf("taskruntime.BuildAgentExec: ReportMode is required")
	}

	taskB64, err := runner.EncodeTask(task)
	if err != nil {
		return nil, fmt.Errorf("encode task: %w", err)
	}

	args := []string{
		"agent", "exec",
		"--task=" + taskB64,
		"--report-mode=" + string(cfg.ReportMode),
	}
	if cfg.MasterURL != "" {
		args = append(args, "--master="+cfg.MasterURL)
	}
	if cfg.Token != "" {
		args = append(args, "--token="+cfg.Token)
	}
	if cfg.StorageURL != "" {
		args = append(args, "--storage-url="+cfg.StorageURL)
	}
	if cfg.OutputDir != "" {
		args = append(args, "--output-dir="+cfg.OutputDir)
	}
	if cfg.InputDir != "" {
		args = append(args, "--input-dir="+cfg.InputDir)
	}
	if cfg.ResultFile != "" {
		args = append(args, "--result-file="+cfg.ResultFile)
	}
	return args, nil
}

// AgentResult is the JSON written to --result-file by piper agent exec.
type AgentResult struct {
	Version int              `json:"version"`
	Result  proto.TaskResult `json:"result"`
}

const agentResultVersion = 1

// WriteAgentResult serializes result to JSON for use in AgentResult files.
func WriteAgentResult(result proto.TaskResult) ([]byte, error) {
	ar := AgentResult{Version: agentResultVersion, Result: result}
	return json.Marshal(ar)
}

// ReadAgentResult decodes an AgentResult from raw JSON bytes.
func ReadAgentResult(data []byte) (proto.TaskResult, error) {
	var ar AgentResult
	if err := json.Unmarshal(data, &ar); err != nil {
		return proto.TaskResult{}, fmt.Errorf("decode agent result: %w", err)
	}
	return ar.Result, nil
}
