package commands

import (
	"context"
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
			if err := cliconfig.ValidateServing(root); err != nil {
				return err
			}
			c, common := root.Workers.Serving, root.Workers.Common
			hostname, id := common.Hostname, c.ID
			if hostname == "" {
				if h, err := os.Hostname(); err == nil {
					hostname = h
				}
			}
			if id == "" {
				id = stableWorkerID("serving", hostname)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w := servingworker.New(servingworker.Config{
				AgentAddr:   common.AgentAddr,
				WorkerToken: common.WorkerToken,
				GPUs:        c.GPUs,
				Hostname:    hostname,
				ID:          id,
				Mode:        c.Mode,
				Docker: servingworker.DockerConfig{
					Image:   c.Docker.Image,
					Network: c.Docker.Network,
				},
			})
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("agent-addr", "", "piper master gRPC agent address, e.g. master:9090 (required)")
	cmd.Flags().String("mode", "", "serving runtime: process or docker")
	cmd.Flags().String("docker-image", "", "default serving image for docker mode")
	cmd.Flags().String("docker-network", "", "Docker network for serving containers")
	cmd.Flags().StringSlice("gpus", nil, "GPU device indices")
	cmd.Flags().String("hostname", "", "hostname reported to master (default: os.Hostname)")
	cmd.Flags().String("id", "", "worker ID (default: stable serving-<hostname>)")
	cmd.Flags().String("worker-token", "", "worker token for gRPC authorization")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	loader.MustBindFlag("workers.common.agent_addr", cmd.Flags().Lookup("agent-addr"))
	loader.MustBindFlag("workers.serving.mode", cmd.Flags().Lookup("mode"))
	loader.MustBindFlag("workers.serving.docker.image", cmd.Flags().Lookup("docker-image"))
	loader.MustBindFlag("workers.serving.docker.network", cmd.Flags().Lookup("docker-network"))
	loader.MustBindFlag("workers.serving.gpus", cmd.Flags().Lookup("gpus"))
	loader.MustBindFlag("workers.common.hostname", cmd.Flags().Lookup("hostname"))
	loader.MustBindFlag("workers.serving.id", cmd.Flags().Lookup("id"))
	loader.MustBindFlag("workers.common.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("workers.common.storage_token", cmd.Flags().Lookup("storage-token"))

	return cmd
}
