package main

import (
	"context"
	"os"
	"time"

	libpiper "github.com/piper/piper/pkg/piper"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [pipeline.yaml]",
		Short: "run a pipeline locally",
		Args:  cobra.ExactArgs(1),
		RunE:  runPipeline,
	}

	cmd.Flags().String("output-dir", "./piper-outputs", "root directory for step outputs")
	cmd.Flags().Int("retries", 2, "max retries per step on failure")
	cmd.Flags().Duration("retry-delay", 5*time.Second, "delay between retries")
	cmd.Flags().Int("concurrency", 0, "max parallel steps (0 = unlimited)")

	viper.BindPFlag("run.output_dir", cmd.Flags().Lookup("output-dir"))
	viper.BindPFlag("run.retries", cmd.Flags().Lookup("retries"))
	viper.BindPFlag("run.retry_delay", cmd.Flags().Lookup("retry-delay"))
	viper.BindPFlag("run.concurrency", cmd.Flags().Lookup("concurrency"))

	return cmd
}

func runPipeline(cmd *cobra.Command, args []string) error {
	cfg := libpiper.Config{
		OutputDir:   viper.GetString("run.output_dir"),
		MaxRetries:  viper.GetInt("run.retries"),
		RetryDelay:  viper.GetDuration("run.retry_delay"),
		Concurrency: viper.GetInt("run.concurrency"),
		Git: libpiper.GitConfig{
			Token: viper.GetString("source.git.token"),
			User:  viper.GetString("source.git.user"),
		},
		S3: libpiper.S3Config{
			Endpoint:  viper.GetString("source.s3.endpoint"),
			AccessKey: viper.GetString("source.s3.access_key"),
			SecretKey: viper.GetString("source.s3.secret_key"),
			Bucket:    viper.GetString("source.s3.bucket"),
		},
	}

	p, err := libpiper.New(cfg)
	if err != nil {
		return err
	}
	defer p.Close()
	result, err := p.RunFile(context.Background(), args[0])
	if err != nil {
		return err
	}

	pipeline.PrintRunResult(result)

	if result.Failed() {
		os.Exit(1)
	}
	return nil
}
