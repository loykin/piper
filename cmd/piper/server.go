package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	libpiper "github.com/piper/piper/pkg/piper"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "start the piper API server",
		RunE:  runServer,
	}

	cmd.Flags().String("addr", ":8080", "listen address")
	cmd.Flags().String("output-dir", "./piper-outputs", "root directory for step outputs")
	cmd.Flags().Bool("tls", false, "enable TLS")
	cmd.Flags().String("tls-cert", "", "TLS certificate file")
	cmd.Flags().String("tls-key", "", "TLS key file")

	viper.BindPFlag("server.addr", cmd.Flags().Lookup("addr"))
	viper.BindPFlag("server.output_dir", cmd.Flags().Lookup("output-dir"))
	viper.BindPFlag("server.tls.enabled", cmd.Flags().Lookup("tls"))
	viper.BindPFlag("server.tls.cert_file", cmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("server.tls.key_file", cmd.Flags().Lookup("tls-key"))

	return cmd
}

func runServer(cmd *cobra.Command, args []string) error {
	cfg := libpiper.DefaultConfig()
	cfg.OutputDir = viper.GetString("server.output_dir")
	cfg.Server = libpiper.ServerConfig{
		Addr: viper.GetString("server.addr"),
		TLS: libpiper.TLSConfig{
			Enabled:  viper.GetBool("server.tls.enabled"),
			CertFile: viper.GetString("server.tls.cert_file"),
			KeyFile:  viper.GetString("server.tls.key_file"),
		},
	}
	cfg.Git = libpiper.GitConfig{
		Token: viper.GetString("source.git.token"),
		User:  viper.GetString("source.git.user"),
	}
	cfg.S3 = libpiper.S3Config{
		Endpoint:  viper.GetString("source.s3.endpoint"),
		AccessKey: viper.GetString("source.s3.access_key"),
		SecretKey: viper.GetString("source.s3.secret_key"),
		Bucket:    viper.GetString("source.s3.bucket"),
	}

	p, err := libpiper.New(cfg)
	if err != nil {
		return err
	}
	defer p.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return p.Serve(ctx, libpiper.ServeOption{})
}
