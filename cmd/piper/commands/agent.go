package commands

import (
	"context"
	"fmt"

	"github.com/piper/piper/pkg/runner"
	"github.com/spf13/cobra"
)

// newAgentCmd returns the "piper agent" command.
// Used as the K8s Job entrypoint: piper agent exec --master=... --step=... -- <command>
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent that executes a step inside a K8s Pod",
	}
	cmd.AddCommand(newAgentExecCmd())
	return cmd
}

type agentExecFlags struct {
	master    string
	token     string
	taskB64   string
	taskID    string
	runID     string
	stepName  string
	stepB64   string
	outputDir string
	inputDir  string
	// S3
	s3Endpoint  string
	s3AccessKey string
	s3SecretKey string
	s3Bucket    string
	s3UseSSL    bool
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
	cmd.Flags().StringVar(&f.s3Endpoint, "s3-endpoint", "", "S3 endpoint")
	cmd.Flags().StringVar(&f.s3AccessKey, "s3-access-key", "", "S3 access key")
	cmd.Flags().StringVar(&f.s3SecretKey, "s3-secret-key", "", "S3 secret key")
	cmd.Flags().StringVar(&f.s3Bucket, "s3-bucket", "", "S3 bucket")
	cmd.Flags().BoolVar(&f.s3UseSSL, "s3-use-ssl", false, "enable SSL for S3")

	return cmd
}

func runAgentExec(ctx context.Context, f agentExecFlags, cmdArgs []string) error {
	task, err := runner.TaskFromAgentInput(f.taskB64, f.taskID, f.runID, f.stepName, f.stepB64, cmdArgs)
	if err != nil {
		return err
	}

	r, err := runner.New(runner.Config{
		MasterURL:   f.master,
		Token:       f.token,
		OutputDir:   f.outputDir,
		InputDir:    f.inputDir,
		S3Endpoint:  f.s3Endpoint,
		S3AccessKey: f.s3AccessKey,
		S3SecretKey: f.s3SecretKey,
		S3Bucket:    f.s3Bucket,
		S3UseSSL:    f.s3UseSSL,
	})
	if err != nil {
		return fmt.Errorf("runner init: %w", err)
	}

	r.Run(ctx, task)
	return nil
}
