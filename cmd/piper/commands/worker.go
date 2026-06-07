package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "start a piper pipeline worker (connects to master via gRPC)",
		RunE: func(cmd *cobra.Command, args []string) error {
			id := viper.GetString("worker.id")
			if id == "" {
				id = worker.NewID("")
			}

			runtime := worker.RuntimeType(viper.GetString("worker.runtime"))
			if runtime == "" {
				runtime = worker.RuntimeBaremetal
			}

			cfg := worker.Config{
				AgentAddr:     viper.GetString("worker.agent_addr"),
				ID:            id,
				Label:         viper.GetString("worker.label"),
				Concurrency:   viper.GetInt("worker.concurrency"),
				Runtime:       runtime,
				MasterURL:     viper.GetString("worker.master"),
				Token:         viper.GetString("worker.token"),
				StorageURL:    resolveStorageURLFromViper(),
				OutputDir:     viper.GetString("worker.output_dir"),
				MetaDir:       viper.GetString("worker.meta_dir"),
				RemoteStore:   viper.GetString("storage.url") != "" || viper.GetString("s3.bucket") != "",
				DefaultImage:  viper.GetString("worker.default_image"),
				DockerNetwork: viper.GetString("worker.docker_network"),
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w, err := worker.New(cfg)
			if err != nil {
				return err
			}
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("agent-addr", "", "gRPC address of piper master agent server (e.g. master:9090)")
	cmd.Flags().String("id", "", "stable worker ID (auto-generated if empty)")
	cmd.Flags().String("label", "", "worker label for task routing")
	cmd.Flags().String("master", "", "piper master HTTP URL (for agent exec callbacks)")
	cmd.Flags().String("token", "", "authentication token")
	cmd.Flags().Int("concurrency", 4, "max parallel tasks")
	cmd.Flags().String("runtime", "baremetal", "execution runtime: baremetal or docker")
	cmd.Flags().String("output-dir", "./piper-outputs", "output directory")
	cmd.Flags().String("meta-dir", "", "metadata sidecar directory (default: $TMPDIR/piper-meta)")
	cmd.Flags().String("default-image", "", "fallback container image (docker runtime)")
	cmd.Flags().String("docker-network", "", "Docker network for step containers")

	mustBindPFlag("worker.agent_addr", cmd.Flags().Lookup("agent-addr"))
	mustBindPFlag("worker.id", cmd.Flags().Lookup("id"))
	mustBindPFlag("worker.label", cmd.Flags().Lookup("label"))
	mustBindPFlag("worker.master", cmd.Flags().Lookup("master"))
	mustBindPFlag("worker.token", cmd.Flags().Lookup("token"))
	mustBindPFlag("worker.concurrency", cmd.Flags().Lookup("concurrency"))
	mustBindPFlag("worker.runtime", cmd.Flags().Lookup("runtime"))
	mustBindPFlag("worker.output_dir", cmd.Flags().Lookup("output-dir"))
	mustBindPFlag("worker.meta_dir", cmd.Flags().Lookup("meta-dir"))
	mustBindPFlag("worker.default_image", cmd.Flags().Lookup("default-image"))
	mustBindPFlag("worker.docker_network", cmd.Flags().Lookup("docker-network"))

	return cmd
}
