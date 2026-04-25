package commands

import (
	"fmt"
	"os"

	piper "github.com/piper/piper"
	"github.com/spf13/cobra"
)

func newParseCmd(p *piper.Piper) *cobra.Command {
	return &cobra.Command{
		Use:   "parse [pipeline.yaml]",
		Short: "validate a pipeline YAML without running it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pl, err := p.ParseFile(args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(os.Stdout, "ok: %s (%d steps)\n", pl.Metadata.Name, len(pl.Spec.Steps))
			return nil
		},
	}
}
