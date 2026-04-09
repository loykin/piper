package cmd

import (
	"context"
	"os"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/piper"
	"github.com/spf13/cobra"
)

func newRunCmd(p *piper.Piper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [pipeline.yaml]",
		Short: "run a pipeline locally",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := p.RunFile(context.Background(), args[0])
			if err != nil {
				return err
			}
			pipeline.PrintRunResult(result)
			if result.Failed() {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().String("output-dir", "./piper-outputs", "root directory for step outputs")
	cmd.Flags().Int("retries", 2, "max retries per step")
	cmd.Flags().Duration("retry-delay", 5*time.Second, "delay between retries")
	cmd.Flags().Int("concurrency", 0, "max parallel steps (0 = unlimited)")

	mustBindPFlag("run.output_dir", cmd.Flags().Lookup("output-dir"))
	mustBindPFlag("run.retries", cmd.Flags().Lookup("retries"))
	mustBindPFlag("run.retry_delay", cmd.Flags().Lookup("retry-delay"))
	mustBindPFlag("run.concurrency", cmd.Flags().Lookup("concurrency"))

	return cmd
}
