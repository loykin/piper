package commands

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	piper "github.com/piper/piper"
	"github.com/spf13/cobra"
)

func newServerCmd(factory PiperFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "start the piper API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := factory()
			if err != nil {
				return err
			}
			defer func() { _ = p.Close() }()

			if p.Config().K8s.AgentImage != "" {
				slog.Info("k8s mode enabled",
					"agent_image", p.Config().K8s.AgentImage,
					"namespace", p.Config().K8s.Namespace,
				)
			}

			if kc, _ := cmd.Flags().GetString("serving-kubeconfig"); kc != "" {
				if err := p.SetServingK8sClientset(kc); err != nil {
					return err
				}
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			addr, _ := cmd.Flags().GetString("addr")
			return p.Serve(ctx, piper.ServeOption{Addr: addr})
		},
	}

	cmd.Flags().String("addr", "", "listen address (default :8080)")
	cmd.Flags().String("token", "", "bearer token required for API and UI requests")
	cmd.Flags().Bool("tls", false, "enable TLS")
	cmd.Flags().String("tls-cert", "", "TLS certificate file")
	cmd.Flags().String("tls-key", "", "TLS key file")
	cmd.Flags().String("serving-kubeconfig", "", "kubeconfig path for ModelService k8s deployments")

	mustBindPFlag("server.addr", cmd.Flags().Lookup("addr"))
	mustBindPFlag("server.token", cmd.Flags().Lookup("token"))
	mustBindPFlag("server.tls.enabled", cmd.Flags().Lookup("tls"))
	mustBindPFlag("server.tls.cert_file", cmd.Flags().Lookup("tls-cert"))
	mustBindPFlag("server.tls.key_file", cmd.Flags().Lookup("tls-key"))

	return cmd
}
