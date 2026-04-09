package main

import (
	"fmt"
	"log/slog"
	"os"

	pipercmd "github.com/piper/piper/pkg/cmd"
	"github.com/piper/piper/pkg/piper"
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
	// piper 인스턴스 생성 — 훅 없음 (standalone 모드)
	p, err := piper.New(buildConfig())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer func() { _ = p.Close() }()

	// pkg/cmd에서 커맨드 가져와 등록
	rootCmd.AddCommand(pipercmd.Commands(p)...)

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

func buildConfig() piper.Config {
	return piper.Config{
		OutputDir:   viper.GetString("run.output_dir"),
		MaxRetries:  viper.GetInt("run.retries"),
		RetryDelay:  viper.GetDuration("run.retry_delay"),
		Concurrency: viper.GetInt("run.concurrency"),
		Git: piper.GitConfig{
			Token: viper.GetString("source.git.token"),
			User:  viper.GetString("source.git.user"),
		},
		S3: piper.S3Config{
			Endpoint:  viper.GetString("source.s3.endpoint"),
			AccessKey: viper.GetString("source.s3.access_key"),
			SecretKey: viper.GetString("source.s3.secret_key"),
			Bucket:    viper.GetString("source.s3.bucket"),
		},
		Server: piper.ServerConfig{
			Addr: viper.GetString("server.addr"),
			TLS: piper.TLSConfig{
				Enabled:  viper.GetBool("server.tls.enabled"),
				CertFile: viper.GetString("server.tls.cert_file"),
				KeyFile:  viper.GetString("server.tls.key_file"),
			},
		},
	}
}
