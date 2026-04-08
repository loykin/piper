package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "piper",
	Short: "lightweight ML pipeline orchestrator",
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.piper.yaml)")
	rootCmd.PersistentFlags().String("log-format", "text", "log format: text | json")
	viper.BindPFlag("log.format", rootCmd.PersistentFlags().Lookup("log-format"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			viper.AddConfigPath(home)
		}
		viper.AddConfigPath(".")
		viper.SetConfigName(".piper")
		viper.SetConfigType("yaml")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "using config:", viper.ConfigFileUsed())
	}

	initLogger()
}

func initLogger() {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}

	if viper.GetString("log.format") == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	slog.SetDefault(slog.New(handler))
}

func main() {
	rootCmd.AddCommand(
		newRunCmd(),
		newWorkerCmd(),
		newServerCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
