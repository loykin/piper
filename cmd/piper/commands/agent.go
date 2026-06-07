package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
	"github.com/piper/piper/pkg/taskruntime"
	"github.com/spf13/cobra"
)

// newAgentCmd returns the "piper agent" command.
// Used as the K8s Job entrypoint: piper agent exec --master=... --task=...
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent that executes a step inside a K8s Pod",
	}
	cmd.AddCommand(newAgentExecCmd())
	return cmd
}

type agentExecFlags struct {
	master     string
	token      string
	taskB64    string
	taskID     string
	runID      string
	stepName   string
	stepB64    string
	outputDir  string
	inputDir   string
	storageURL string
	resultFile string
	reportMode string
	gitUser    string
	gitToken   string
}

func newAgentExecCmd() *cobra.Command {
	var f agentExecFlags

	cmd := &cobra.Command{
		Use:   "exec [flags] -- <command...>",
		Short: "Execute a step and write the result",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentExec(cmd.Context(), f, args)
		},
	}

	cmd.Flags().StringVar(&f.master, "master", "", "piper server URL")
	cmd.Flags().StringVar(&f.token, "token", "", "auth token")
	cmd.Flags().StringVar(&f.taskB64, "task", "", "base64-encoded proto.Task JSON")
	cmd.Flags().StringVar(&f.taskID, "task-id", "", "task ID")
	cmd.Flags().StringVar(&f.runID, "run-id", "", "run ID")
	cmd.Flags().StringVar(&f.stepName, "step-name", "", "step name")
	cmd.Flags().StringVar(&f.stepB64, "step", "", "base64-encoded pipeline.Step JSON")
	cmd.Flags().StringVar(&f.outputDir, "output-dir", "/piper-outputs", "local output directory")
	cmd.Flags().StringVar(&f.inputDir, "input-dir", "/piper-inputs", "local input directory")
	cmd.Flags().StringVar(&f.storageURL, "storage-url", "", "artifact store URL (s3://, file://, http://)")
	cmd.Flags().StringVar(&f.resultFile, "result-file", "", "path to write AgentResult JSON (required for report-mode=file)")
	cmd.Flags().StringVar(&f.reportMode, "report-mode", string(taskruntime.ReportModeHTTP), "result delivery mode: http (migration) or file")

	return cmd
}

func runAgentExec(ctx context.Context, f agentExecFlags, cmdArgs []string) error {
	// Git credentials: prefer flags, fall back to environment variables.
	gitUser := f.gitUser
	if gitUser == "" {
		gitUser = os.Getenv("PIPER_GIT_USER")
	}
	gitToken := f.gitToken
	if gitToken == "" {
		gitToken = os.Getenv("PIPER_GIT_TOKEN")
	}

	task, err := runner.TaskFromAgentInput(f.taskB64, f.taskID, f.runID, f.stepName, f.stepB64, cmdArgs)
	if err != nil {
		return err
	}

	r, err := runner.New(runner.Config{
		MasterURL:  f.master,
		Token:      f.token,
		OutputDir:  f.outputDir,
		InputDir:   f.inputDir,
		StorageURL: f.storageURL,
		GitUser:    gitUser,
		GitToken:   gitToken,
	})
	if err != nil {
		return fmt.Errorf("runner init: %w", err)
	}

	result := r.Run(ctx, task)

	if err := deliverResult(result, taskruntime.ReportMode(f.reportMode), f.resultFile, r); err != nil {
		return fmt.Errorf("deliver result: %w", err)
	}

	if result.Status == proto.TaskStatusFailed {
		return fmt.Errorf("step %q failed: %s", task.StepName, result.Error)
	}
	return nil
}

// deliverResult sends the TaskResult according to the configured report mode.
func deliverResult(result proto.TaskResult, mode taskruntime.ReportMode, resultFile string, r *runner.Runner) error {
	switch mode {
	case taskruntime.ReportModeFile:
		if resultFile == "" {
			return fmt.Errorf("--result-file is required for report-mode=file")
		}
		return writeResultFile(resultFile, result)
	default: // ReportModeHTTP and legacy zero value
		r.Report(result)
		return nil
	}
}

// writeResultFile atomically writes an AgentResult JSON to path.
// For /dev/termination-log (K8s) the result is truncated to fit the 4096-byte
// limit before writing, and atomic rename is skipped (char device).
func writeResultFile(path string, result proto.TaskResult) error {
	if path == "/dev/termination-log" {
		result = truncateForTerminationLog(result)
		data, err := taskruntime.WriteAgentResult(result)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0644)
	}
	data, err := taskruntime.WriteAgentResult(result)
	if err != nil {
		return err
	}
	// Atomic write via rename for regular files.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".result-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp result file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write result file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// truncateForTerminationLog trims an AgentResult JSON to fit within
// Kubernetes termination message limit (4096 bytes).
func truncateForTerminationLog(result proto.TaskResult) proto.TaskResult {
	const (
		maxError  = 2048
		softLimit = 3584
		hardLimit = 4096
	)
	if len(result.Error) > maxError {
		result.Error = result.Error[:maxError] + "... [truncated]"
	}
	data, err := json.Marshal(taskruntime.AgentResult{Version: 1, Result: result})
	if err != nil || len(data) <= softLimit {
		return result
	}
	// Shrink error further until it fits.
	for len(result.Error) > 0 && len(data) > softLimit {
		cut := len(result.Error) / 2
		if cut == 0 {
			break
		}
		result.Error = result.Error[:cut] + "... [truncated]"
		data, err = json.Marshal(taskruntime.AgentResult{Version: 1, Result: result})
		if err != nil {
			break
		}
	}
	if len(data) > hardLimit {
		result.Error = "task failed; detail exceeded termination message limit"
	}
	return result
}
