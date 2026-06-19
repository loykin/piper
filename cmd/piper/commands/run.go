package commands

import (
	"context"
	"os"
	"time"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/spf13/cobra"
)

func newRunCmd(loader *cliconfig.Loader, factory PiperFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [pipeline.yaml]",
		Short: "run a pipeline locally",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := factory()
			if err != nil {
				return err
			}
			defer func() { _ = p.Close() }()

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

	cmd.Flags().String("output-dir", "", "root directory for step outputs")
	cmd.Flags().Int("retries", 0, "max retries per step")
	cmd.Flags().Duration("retry-delay", 0*time.Second, "delay between retries")
	cmd.Flags().Int("concurrency", 0, "max parallel steps (0 = unlimited)")

	loader.MustBindFlag("server.run.output_dir", cmd.Flags().Lookup("output-dir"))
	loader.MustBindFlag("server.run.retries", cmd.Flags().Lookup("retries"))
	loader.MustBindFlag("server.run.retry_delay", cmd.Flags().Lookup("retry-delay"))
	loader.MustBindFlag("server.run.concurrency", cmd.Flags().Lookup("concurrency"))

	return cmd
}
