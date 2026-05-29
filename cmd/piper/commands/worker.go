package commands

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
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read source config directly from viper (which is fully initialized
			// by initConfig at this point) rather than from p.SourceConfig(), which
			// was built before viper loaded the config file.
			srcCfg := source.Config{
				GitUser:     viper.GetString("source.git.user"),
				GitToken:    viper.GetString("source.git.token"),
				S3Endpoint:  viper.GetString("source.s3.endpoint"),
				S3AccessKey: viper.GetString("source.s3.access_key"),
				S3SecretKey: viper.GetString("source.s3.secret_key"),
				S3Bucket:    viper.GetString("source.s3.bucket"),
				S3UseSSL:    viper.GetBool("source.s3.use_ssl"),
			}
			cfg := workerConfigFromSource(worker.Config{
				MasterURL:           viper.GetString("worker.master"),
				Label:               viper.GetString("worker.label"),
				Token:               viper.GetString("worker.token"),
				Version:             viper.GetString("worker.version"),
				Capabilities:        viper.GetStringSlice("worker.capabilities"),
				PollInterval:        viper.GetDuration("worker.poll_interval"),
				ShutdownGracePeriod: viper.GetDuration("worker.shutdown_grace_period"),
				OutputDir:           viper.GetString("worker.output_dir"),
				Concurrency:         viper.GetInt("worker.concurrency"),
			}, srcCfg)

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w, err := worker.New(cfg)
			if err != nil {
				return err
			}
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("master", "", "master server URL")
	cmd.Flags().String("label", "", "worker label (e.g. gpu)")
	cmd.Flags().String("token", "", "authentication token")
	cmd.Flags().String("version", "", "worker version")
	cmd.Flags().StringSlice("capability", nil, "worker capability; may be repeated")
	cmd.Flags().Duration("poll-interval", 3*time.Second, "polling interval")
	cmd.Flags().Duration("shutdown-grace-period", 30*time.Second, "time to wait for in-flight tasks before canceling them")
	cmd.Flags().String("output-dir", "./piper-outputs", "output directory")
	cmd.Flags().Int("concurrency", 4, "max parallel tasks")
	_ = cmd.MarkFlagRequired("master")

	mustBindPFlag("worker.master", cmd.Flags().Lookup("master"))
	mustBindPFlag("worker.label", cmd.Flags().Lookup("label"))
	mustBindPFlag("worker.token", cmd.Flags().Lookup("token"))
	mustBindPFlag("worker.version", cmd.Flags().Lookup("version"))
	mustBindPFlag("worker.capabilities", cmd.Flags().Lookup("capability"))
	mustBindPFlag("worker.poll_interval", cmd.Flags().Lookup("poll-interval"))
	mustBindPFlag("worker.shutdown_grace_period", cmd.Flags().Lookup("shutdown-grace-period"))
	mustBindPFlag("worker.output_dir", cmd.Flags().Lookup("output-dir"))
	mustBindPFlag("worker.concurrency", cmd.Flags().Lookup("concurrency"))

	return cmd
}

func workerConfigFromSource(cfg worker.Config, srcCfg source.Config) worker.Config {
	cfg.GitUser = srcCfg.GitUser
	cfg.GitToken = srcCfg.GitToken
	cfg.S3Endpoint = srcCfg.S3Endpoint
	cfg.S3AccessKey = srcCfg.S3AccessKey
	cfg.S3SecretKey = srcCfg.S3SecretKey
	cfg.S3Bucket = srcCfg.S3Bucket
	cfg.S3UseSSL = srcCfg.S3UseSSL
	return cfg
}
