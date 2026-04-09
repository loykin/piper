package main

import (
	"fmt"
	"log/slog"
	"os"

	pipercmd "github.com/piper/piper/pkg/cmd"
	"github.com/piper/piper/pkg/k8s"
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
	cfg := buildConfig()

	// piper 인스턴스 생성 — 훅 없음 (standalone 모드)
	p, err := piper.New(cfg)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer func() { _ = p.Close() }()

	// K8s 모드: AgentImage가 설정된 경우 K8s Launcher를 Dispatcher로 등록
	if cfg.K8s.AgentImage != "" {
		launcher, err := k8s.New(k8s.Config{
			AgentImage:   cfg.K8s.AgentImage,
			Namespace:    cfg.K8s.Namespace,
			InCluster:    cfg.K8s.InCluster,
			Kubeconfig:   cfg.K8s.Kubeconfig,
			MasterURL:    cfg.K8s.MasterURL,
			Token:        cfg.K8s.Token,
			DefaultImage: cfg.K8s.DefaultImage,
			S3Endpoint:   cfg.S3.Endpoint,
			S3AccessKey:  cfg.S3.AccessKey,
			S3SecretKey:  cfg.S3.SecretKey,
			S3Bucket:     cfg.S3.Bucket,
			S3UseSSL:     cfg.S3.UseSSL,
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "k8s launcher error:", err)
			os.Exit(1)
		}
		p.SetDispatcher(launcher)
		slog.Info("k8s mode enabled", "agent_image", cfg.K8s.AgentImage, "namespace", cfg.K8s.Namespace)
	}

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
			UseSSL:    viper.GetBool("source.s3.use_ssl"),
		},
		Server: piper.ServerConfig{
			Addr: viper.GetString("server.addr"),
			TLS: piper.TLSConfig{
				Enabled:  viper.GetBool("server.tls.enabled"),
				CertFile: viper.GetString("server.tls.cert_file"),
				KeyFile:  viper.GetString("server.tls.key_file"),
			},
		},
		K8s: piper.K8sConfig{
			AgentImage:       viper.GetString("k8s.agent_image"),
			Namespace:        viper.GetString("k8s.namespace"),
			InCluster:        viper.GetBool("k8s.in_cluster"),
			Kubeconfig:       viper.GetString("k8s.kubeconfig"),
			MasterURL:        viper.GetString("k8s.master_url"),
			Token:            viper.GetString("k8s.token"),
			DefaultImage:     viper.GetString("k8s.default_image"),
			TTLAfterFinished: int32(viper.GetInt("k8s.ttl_after_finished")),
		},
	}
}
