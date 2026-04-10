package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/piper/piper/pkg/piper"
	"github.com/piper/piper/pkg/worker"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newWorkerCmd(p *piper.Piper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "start a piper worker (polls master for tasks)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := worker.Config{
				MasterURL:    viper.GetString("worker.master"),
				Label:        viper.GetString("worker.label"),
				Token:        viper.GetString("worker.token"),
				PollInterval: viper.GetDuration("worker.poll_interval"),
				OutputDir:    viper.GetString("worker.output_dir"),
				Concurrency:  viper.GetInt("worker.concurrency"),
				SourceCfg:    p.SourceConfig(),
			}

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
	cmd.Flags().Duration("poll-interval", 3*time.Second, "polling interval")
	cmd.Flags().String("output-dir", "./piper-outputs", "output directory")
	cmd.Flags().Int("concurrency", 4, "max parallel tasks")
	_ = cmd.MarkFlagRequired("master")

	mustBindPFlag("worker.master", cmd.Flags().Lookup("master"))
	mustBindPFlag("worker.label", cmd.Flags().Lookup("label"))
	mustBindPFlag("worker.token", cmd.Flags().Lookup("token"))
	mustBindPFlag("worker.poll_interval", cmd.Flags().Lookup("poll-interval"))
	mustBindPFlag("worker.output_dir", cmd.Flags().Lookup("output-dir"))
	mustBindPFlag("worker.concurrency", cmd.Flags().Lookup("concurrency"))

	return cmd
}
