package main

import (
	"fmt"
	"log/slog"
	"os"

	pipercmd "github.com/piper/piper/cmd/piper/commands"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	mustBindPFlag("log.format", rootCmd.PersistentFlags().Lookup("log-format"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, _ := os.UserHomeDir()
		if home != "" {
			viper.AddConfigPath(home)
		}
		viper.AddConfigPath(".")
		viper.SetConfigName(".piper")
		viper.SetConfigType("yaml")
	}
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err == nil {
		_, _ = fmt.Fprintln(os.Stderr, "using config:", viper.ConfigFileUsed())
	}
	initLogger()
}

func initLogger() {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var handler slog.Handler
	if viper.GetString("log.format") == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func main() {
	rootCmd.AddCommand(pipercmd.Commands(pipercmd.NewPiper)...)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func mustBindPFlag(key string, flag *pflag.Flag) {
	if flag == nil {
		panic(fmt.Sprintf("mustBindPFlag: flag for key %q is nil", key))
	}
	if err := viper.BindPFlag(key, flag); err != nil {
		panic(fmt.Sprintf("mustBindPFlag(%q): %v", key, err))
	}
}
