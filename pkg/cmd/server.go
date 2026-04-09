package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/piper/piper/pkg/piper"
	"github.com/spf13/cobra"
)

func newServerCmd(p *piper.Piper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "start the piper API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return p.Serve(ctx, piper.ServeOption{})
		},
	}

	cmd.Flags().String("addr", ":8080", "listen address")
	cmd.Flags().Bool("tls", false, "enable TLS")
	cmd.Flags().String("tls-cert", "", "TLS certificate file")
	cmd.Flags().String("tls-key", "", "TLS key file")

	mustBindPFlag("server.addr", cmd.Flags().Lookup("addr"))
	mustBindPFlag("server.tls.enabled", cmd.Flags().Lookup("tls"))
	mustBindPFlag("server.tls.cert_file", cmd.Flags().Lookup("tls-cert"))
	mustBindPFlag("server.tls.key_file", cmd.Flags().Lookup("tls-key"))

	return cmd
}
