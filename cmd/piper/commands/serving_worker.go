package commands

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	servingworker "github.com/piper/piper/pkg/serving/worker"
)

func newServingWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serving-worker",
		Short: "Start a serving worker agent on this node",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentAddr, _ := cmd.Flags().GetString("agent-addr")
			mode, _ := cmd.Flags().GetString("mode")
			dockerImage, _ := cmd.Flags().GetString("docker-image")
			dockerNetwork, _ := cmd.Flags().GetString("docker-network")
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
				AgentAddr:   agentAddr,
				WorkerToken: viper.GetString("worker.token"),
				GPUs:        gpus,
				Hostname:    hostname,
				ID:          id,
				Mode:        mode,
				Docker: servingworker.DockerConfig{
					Image:   dockerImage,
					Network: dockerNetwork,
				},
			})
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("agent-addr", "", "piper master gRPC agent address, e.g. master:9090 (required)")
	cmd.Flags().String("mode", "process", "serving runtime: process or docker")
	cmd.Flags().String("docker-image", "", "default serving image for docker mode")
	cmd.Flags().String("docker-network", "bridge", "Docker network for serving containers")
	cmd.Flags().String("gpus", "", "comma-separated GPU device indices (e.g. 0,1)")
	cmd.Flags().String("hostname", "", "hostname reported to master (default: os.Hostname)")
	cmd.Flags().String("id", "", "worker ID (default: stable serving-<hostname>)")
	_ = cmd.MarkFlagRequired("agent-addr")

	return cmd
}
