package commands

import (
	"context"
	"os"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/spf13/cobra"
)

func newRunCmd(loader *cliconfig.Loader, factory PiperFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "run [pipeline.yaml]",
		Short:   "run a pipeline locally",
		Args:    cobra.ExactArgs(1),
		PreRunE: makePreRunE(loader),
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

	return cmd
}
