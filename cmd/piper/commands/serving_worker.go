package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/spf13/cobra"

	servingworker "github.com/piper/piper/pkg/serving/worker"
)

func newServingWorkerCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serving-worker",
		Short: "Start a serving worker agent on this node",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			if err := applyServingFlags(cmd, &root); err != nil {
				return err
			}
			selection, err := cliconfig.SelectServing(root)
			if err != nil {
				return err
			}
			common := root.Worker
			hostname := common.Hostname
			if hostname == "" {
				if h, err := os.Hostname(); err == nil {
					hostname = h
				}
			}
			id, err := loadOrCreateWorkerID(common.StateDir, "serving-"+selection.Infrastructure)
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w := servingworker.New(servingworker.Config{
				MasterURL:      common.MasterURL,
				WorkerToken:    common.WorkerToken,
				Hostname:       hostname,
				ID:             id,
				Labels:         common.Labels,
				Infrastructure: selection.Infrastructure,
				Docker: servingworker.DockerConfig{
					Network: selection.DockerNetwork,
				},
			})
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("master-url", "", "piper master HTTP(S) URL (required)")
	cmd.Flags().String("infrastructure", "", "worker infrastructure: baremetal or docker")
	cmd.Flags().String("docker-network", "", "Docker network for serving containers")
	cmd.Flags().String("hostname", "", "hostname reported to master (default: os.Hostname)")
	cmd.Flags().String("state-dir", "", "directory for persistent worker identity and state")
	cmd.Flags().String("worker-token", "", "worker token for gRPC authorization")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	loader.MustBindFlag("worker.master_url", cmd.Flags().Lookup("master-url"))
	loader.MustBindFlag("worker.hostname", cmd.Flags().Lookup("hostname"))
	loader.MustBindFlag("worker.state_dir", cmd.Flags().Lookup("state-dir"))
	loader.MustBindFlag("worker.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("worker.storage_token", cmd.Flags().Lookup("storage-token"))

	return cmd
}

func applyServingFlags(cmd *cobra.Command, root *cliconfig.RootConfig) error {
	mode, _ := cmd.Flags().GetString("infrastructure")
	if cmd.Flags().Changed("infrastructure") {
		switch mode {
		case cliconfig.InfrastructureBaremetal:
			if root.Worker.Docker != nil || root.Worker.K8s != nil {
				return fmt.Errorf("--infrastructure conflicts with configured worker infrastructure")
			}
			if root.Worker.Baremetal == nil {
				root.Worker.Baremetal = &cliconfig.BaremetalWorkerConfig{}
			}
			if root.Worker.Capabilities.Serving == nil {
				root.Worker.Capabilities.Serving = &cliconfig.ServingCapabilityConfig{}
			}
		case cliconfig.InfrastructureDocker:
			if root.Worker.Baremetal != nil || root.Worker.K8s != nil {
				return fmt.Errorf("--infrastructure conflicts with configured worker infrastructure")
			}
			if root.Worker.Docker == nil {
				root.Worker.Docker = &cliconfig.DockerWorkerConfig{}
			}
			if root.Worker.Capabilities.Serving == nil {
				root.Worker.Capabilities.Serving = &cliconfig.ServingCapabilityConfig{}
			}
		default:
			return fmt.Errorf("--infrastructure must be baremetal or docker")
		}
	}
	if root.Worker.Docker != nil {
		if cmd.Flags().Changed("docker-network") {
			root.Worker.Docker.Network, _ = cmd.Flags().GetString("docker-network")
		}
	}
	return cliconfig.ValidateServing(*root)
}
