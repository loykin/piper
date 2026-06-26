// Package taskruntime provides the RuntimeDriver interface and shared
// utilities for executing pipeline steps across different environments.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/piper/piper/internal/proto"
)

// AgentExecConfig holds the parameters needed to build a piper agent exec
// argument list. It is environment-agnostic; each RuntimeDriver supplies
// the paths it mounts or creates.
type AgentExecConfig struct {
	StorageToken string
	StorageURL   string
	OutputDir    string
	InputDir     string
	// ResultFile is the path inside the execution environment where the agent
	// writes the AgentResult JSON.
	ResultFile string
}

// BuildAgentExec returns the argv slice for `piper agent exec`, not including
// the binary itself. The task is base64-encoded and passed as --task.
// Returns an error if ResultFile is empty or the task cannot be encoded.
func BuildAgentExec(task *proto.Task, cfg AgentExecConfig) ([]string, error) {
	if cfg.ResultFile == "" {
		return nil, fmt.Errorf("taskruntime.BuildAgentExec: ResultFile is required")
	}

	taskB64, err := EncodeTask(task)
	if err != nil {
		return nil, fmt.Errorf("encode task: %w", err)
	}

	args := []string{
		"agent", "exec",
		"--task=" + taskB64,
		"--result-file=" + cfg.ResultFile,
	}
	if cfg.StorageToken != "" {
		args = append(args, "--storage-token="+cfg.StorageToken)
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

// DeliverResult writes a TaskResult for the parent worker to forward.
func DeliverResult(result proto.TaskResult, resultFile string) error {
	if resultFile == "" {
		return fmt.Errorf("result file path is required")
	}
	return WriteResultFile(resultFile, result)
}

// WriteResultFile atomically writes an AgentResult JSON to path.
// For /dev/termination-log (K8s) atomic rename is skipped (char device).
func WriteResultFile(path string, result proto.TaskResult) error {
	data, err := WriteAgentResult(result)
	if err != nil {
		return err
	}
	if path == "/dev/termination-log" {
		return writeFile(path, data)
	}
	return writeFileAtomic(path, data)
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".result-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp result file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
