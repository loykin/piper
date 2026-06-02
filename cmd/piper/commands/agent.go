package commands

import (
	"context"
	"fmt"

	"github.com/piper/piper/pkg/runner"
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
}

func newAgentExecCmd() *cobra.Command {
	var f agentExecFlags

	cmd := &cobra.Command{
		Use:   "exec [flags] -- <command...>",
		Short: "Execute a step and report the result to the master",
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

	return cmd
}

func runAgentExec(ctx context.Context, f agentExecFlags, cmdArgs []string) error {
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
	})
	if err != nil {
		return fmt.Errorf("runner init: %w", err)
	}

	if !r.Run(ctx, task) {
		return fmt.Errorf("step %q failed", task.StepName)
	}
	return nil
}
