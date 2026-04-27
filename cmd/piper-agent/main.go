package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/piper/piper/pkg/runner"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "piper-agent",
		Short: "piper agent — executes a step inside a K8s Pod",
	}
	root.AddCommand(newExecCmd())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

type execFlags struct {
	master, token, taskB64, taskID, runID, stepName, stepB64, outputDir, inputDir string
	s3Endpoint, s3AccessKey, s3SecretKey, s3Bucket                                string
	s3UseSSL                                                                      bool
}

func newExecCmd() *cobra.Command {
	var f execFlags
	cmd := &cobra.Command{
		Use:   "exec [flags] -- <command...>",
		Short: "Execute a step and report result to master",
		RunE: func(cmd *cobra.Command, args []string) error {
			task, err := runner.TaskFromAgentInput(f.taskB64, f.taskID, f.runID, f.stepName, f.stepB64, args)
			if err != nil {
				return err
			}
			r, err := runner.New(runner.Config{
				MasterURL: f.master, Token: f.token,
				OutputDir: f.outputDir, InputDir: f.inputDir,
				S3Endpoint: f.s3Endpoint, S3AccessKey: f.s3AccessKey,
				S3SecretKey: f.s3SecretKey, S3Bucket: f.s3Bucket, S3UseSSL: f.s3UseSSL,
			})
			if err != nil {
				return fmt.Errorf("runner init: %w", err)
			}
			r.Run(cmd.Context(), task)
			return nil
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
