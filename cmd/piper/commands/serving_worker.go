package commands

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	servingworker "github.com/piper/piper/pkg/workers/baremetal/serving"
)

func newServingWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serving-worker",
		Short: "Start a serving worker agent on this node",
		RunE: func(cmd *cobra.Command, args []string) error {
			masterURL, _ := cmd.Flags().GetString("master")
			addr, _ := cmd.Flags().GetString("addr")
			advertiseAddr, _ := cmd.Flags().GetString("advertise-addr")
			tlsCert, _ := cmd.Flags().GetString("tls-cert")
			tlsKey, _ := cmd.Flags().GetString("tls-key")
			gpusStr, _ := cmd.Flags().GetString("gpus")
			hostname, _ := cmd.Flags().GetString("hostname")
			id, _ := cmd.Flags().GetString("id")

			var gpus []string
			if gpusStr != "" {
				for _, g := range strings.Split(gpusStr, ",") {
					if t := strings.TrimSpace(g); t != "" {
						gpus = append(gpus, t)
					}
				}
			}

			if id == "" {
				id = uuid.NewString()
			}
			if hostname == "" {
				if h, err := os.Hostname(); err == nil {
					hostname = h
				}
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w := servingworker.New(servingworker.Config{
				MasterURL:     masterURL,
				Addr:          addr,
				AdvertiseAddr: advertiseAddr,
				TLSCert:       tlsCert,
				TLSKey:        tlsKey,
				GPUs:          gpus,
				Hostname:      hostname,
				ID:            id,
			})
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("master", "", "master server URL (required)")
	cmd.Flags().String("addr", ":7700", "listen address for this worker")
	cmd.Flags().String("advertise-addr", "", "URL advertised to master (default: derived from --addr)")
	cmd.Flags().String("tls-cert", "", "TLS certificate file (enables HTTPS)")
	cmd.Flags().String("tls-key", "", "TLS private key file (enables HTTPS)")
	cmd.Flags().String("gpus", "", "comma-separated GPU device indices (e.g. 0,1)")
	cmd.Flags().String("hostname", "", "hostname reported to master (default: os.Hostname)")
	cmd.Flags().String("id", "", "worker ID (default: random UUID)")
	_ = cmd.MarkFlagRequired("master")

	return cmd
}
