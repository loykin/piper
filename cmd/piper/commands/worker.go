package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	worker "github.com/piper/piper/pkg/pipeline/worker"
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
			workerToken := viper.GetString("worker.token")
			storageToken := viper.GetString("storage.token")

			cfg := worker.Config{
				Agent: worker.AgentConfig{
					Addr:        viper.GetString("worker.agent_addr"),
					WorkerToken: workerToken,
					ID:          id,
					Label:       viper.GetString("worker.label"),
					Concurrency: viper.GetInt("worker.concurrency"),
				},
				Store: worker.StoreConfig{
					MasterURL:    viper.GetString("worker.master"),
					WorkerToken:  workerToken,
					StorageToken: storageToken,
					StorageURL:   resolveStorageURLFromViper(),
					OutputDir:    viper.GetString("worker.output_dir"),
					RemoteStore:  viper.GetString("storage.url") != "" || viper.GetString("s3.bucket") != "",
					GitUser:      viper.GetString("git.user"),
					GitToken:     viper.GetString("git.token"),
				},
				Runtime: runtime,
				Baremetal: worker.BaremetalConfig{
					MetaDir: viper.GetString("worker.meta_dir"),
				},
				Docker: worker.DockerConfig{
					DefaultImage: viper.GetString("worker.default_image"),
					Network:      viper.GetString("worker.docker_network"),
				},
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
	cmd.Flags().String("worker-token", "", "worker token for gRPC and master callbacks")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
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
	mustBindPFlag("worker.token", cmd.Flags().Lookup("worker-token"))
	mustBindPFlag("storage.token", cmd.Flags().Lookup("storage-token"))
	mustBindPFlag("worker.concurrency", cmd.Flags().Lookup("concurrency"))
	mustBindPFlag("worker.runtime", cmd.Flags().Lookup("runtime"))
	mustBindPFlag("worker.output_dir", cmd.Flags().Lookup("output-dir"))
	mustBindPFlag("worker.meta_dir", cmd.Flags().Lookup("meta-dir"))
	mustBindPFlag("worker.default_image", cmd.Flags().Lookup("default-image"))
	mustBindPFlag("worker.docker_network", cmd.Flags().Lookup("docker-network"))

	return cmd
}
