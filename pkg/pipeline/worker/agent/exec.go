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
	MasterURL    string
	WorkerToken  string
	StorageToken string
	StorageURL   string
	OutputDir    string
	InputDir     string
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

	taskB64, err := EncodeTask(task)
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
	if cfg.WorkerToken != "" {
		args = append(args, "--worker-token="+cfg.WorkerToken)
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

// DeliverResult sends a TaskResult according to the configured ReportMode.
// ReportModeFile writes to resultFile atomically; ReportModeHTTP posts to master.
func DeliverResult(result proto.TaskResult, mode ReportMode, resultFile string, r *Runner) error {
	switch mode {
	case ReportModeFile:
		if resultFile == "" {
			return fmt.Errorf("result file path required for report-mode=file")
		}
		return WriteResultFile(resultFile, result)
	default:
		r.Report(result)
		return nil
	}
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
