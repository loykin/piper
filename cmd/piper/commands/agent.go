package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
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
	cmd.Flags().StringVar(&f.reportMode, "report-mode", string(agent.ReportModeHTTP), "result delivery mode: http (migration) or file")

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

	task, err := agent.TaskFromAgentInput(f.taskB64, f.taskID, f.runID, f.stepName, f.stepB64, cmdArgs)
	if err != nil {
		return err
	}

	r, err := agent.New(agent.Config{
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

	if err := deliverResult(result, agent.ReportMode(f.reportMode), f.resultFile, r); err != nil {
		return fmt.Errorf("deliver result: %w", err)
	}

	if result.Status == proto.TaskStatusFailed {
		return fmt.Errorf("step %q failed: %s", task.StepName, result.Error)
	}
	return nil
}

// deliverResult sends the TaskResult according to the configured report mode.
// For /dev/termination-log, the result is first truncated to fit the 4096-byte K8s limit.
func deliverResult(result proto.TaskResult, mode agent.ReportMode, resultFile string, r *agent.Runner) error {
	if resultFile == "/dev/termination-log" {
		result = truncateForTerminationLog(result)
	}
	return agent.DeliverResult(result, mode, resultFile, r)
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
	data, err := json.Marshal(agent.AgentResult{Version: 1, Result: result})
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
		data, err = json.Marshal(agent.AgentResult{Version: 1, Result: result})
		if err != nil {
			break
		}
	}
	if len(data) > hardLimit {
		result.Error = "task failed; detail exceeded termination message limit"
	}
	return result
}
