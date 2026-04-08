package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/piper/piper/pkg/source"
	"github.com/piper/piper/pkg/worker"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "start a piper worker (polls master for tasks)",
		RunE:  runWorker,
	}

	cmd.Flags().String("master", "", "master server URL (e.g. http://piper.internal:8080)")
	cmd.Flags().String("label", "", "worker label for task routing (e.g. gpu)")
	cmd.Flags().String("token", "", "authentication token")
	cmd.Flags().Duration("poll-interval", 3*time.Second, "polling interval")
	cmd.Flags().String("output-dir", "./piper-outputs", "root directory for step outputs")
	cmd.Flags().Int("concurrency", 4, "max parallel tasks per worker")
	cmd.Flags().String("git-token", "", "git HTTP token for private repos")
	cmd.Flags().String("git-user", "git", "git HTTP username")
	cmd.Flags().String("s3-endpoint", "", "S3/MinIO endpoint")
	cmd.Flags().String("s3-access-key", "", "S3 access key")
	cmd.Flags().String("s3-secret-key", "", "S3 secret key")
	cmd.Flags().String("s3-bucket", "", "default S3 bucket")
	cmd.MarkFlagRequired("master")

	viper.BindPFlag("worker.master", cmd.Flags().Lookup("master"))
	viper.BindPFlag("worker.label", cmd.Flags().Lookup("label"))
	viper.BindPFlag("worker.token", cmd.Flags().Lookup("token"))
	viper.BindPFlag("worker.poll_interval", cmd.Flags().Lookup("poll-interval"))
	viper.BindPFlag("worker.output_dir", cmd.Flags().Lookup("output-dir"))
	viper.BindPFlag("worker.concurrency", cmd.Flags().Lookup("concurrency"))
	viper.BindPFlag("source.git.token", cmd.Flags().Lookup("git-token"))
	viper.BindPFlag("source.git.user", cmd.Flags().Lookup("git-user"))
	viper.BindPFlag("source.s3.endpoint", cmd.Flags().Lookup("s3-endpoint"))
	viper.BindPFlag("source.s3.access_key", cmd.Flags().Lookup("s3-access-key"))
	viper.BindPFlag("source.s3.secret_key", cmd.Flags().Lookup("s3-secret-key"))
	viper.BindPFlag("source.s3.bucket", cmd.Flags().Lookup("s3-bucket"))

	return cmd
}

func runWorker(cmd *cobra.Command, args []string) error {
	cfg := worker.Config{
		MasterURL:    viper.GetString("worker.master"),
		Label:        viper.GetString("worker.label"),
		Token:        viper.GetString("worker.token"),
		PollInterval: viper.GetDuration("worker.poll_interval"),
		OutputDir:    viper.GetString("worker.output_dir"),
		Concurrency:  viper.GetInt("worker.concurrency"),
		SourceCfg: source.Config{
			GitToken:    viper.GetString("source.git.token"),
			GitUser:     viper.GetString("source.git.user"),
			S3Endpoint:  viper.GetString("source.s3.endpoint"),
			S3AccessKey: viper.GetString("source.s3.access_key"),
			S3SecretKey: viper.GetString("source.s3.secret_key"),
			S3Bucket:    viper.GetString("source.s3.bucket"),
		},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	w := worker.New(cfg)
	return w.Run(ctx)
}
