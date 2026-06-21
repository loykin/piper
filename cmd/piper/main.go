package main

import (
	"os"

	piper "github.com/piper/piper"
	pipercmd "github.com/piper/piper/cmd/piper/commands"
	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/spf13/cobra"
)

var cfgFile string
var loader = cliconfig.NewLoader()

var rootCmd = &cobra.Command{
	Use:   "piper",
	Short: "lightweight ML pipeline orchestrator",
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		loader.SetConfigFile(cfgFile)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.piper.yaml)")
	rootCmd.PersistentFlags().String("log-format", "", "log format: text | json")
	loader.MustBindFlag("log.format", rootCmd.PersistentFlags().Lookup("log-format"))
}

func main() {
	factory := func() (*piper.Piper, error) { return pipercmd.NewPiper(loader) }
	rootCmd.AddCommand(pipercmd.Commands(loader, factory)...)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
