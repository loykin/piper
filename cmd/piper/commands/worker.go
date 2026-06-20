package commands

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	worker "github.com/piper/piper/pkg/pipeline/worker"
	"github.com/spf13/cobra"
)

func newWorkerCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "start a piper pipeline worker (connects to master via gRPC)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			if err := cliconfig.ValidatePipeline(root); err != nil {
				return err
			}
			c := root.Workers.Pipeline
			common := root.Workers.Common
			id, err := loadOrCreateWorkerID(common.StateDir, "pipeline")
			if err != nil {
				return err
			}

			runtime := worker.RuntimeType(c.Runtime)
			storageToken := common.StorageToken
			if storageToken == "" {
				storageToken = root.Storage.Token
			}

			cfg := worker.Config{
				Agent: worker.AgentConfig{
					MasterURL:   common.MasterURL,
					WorkerToken: common.WorkerToken,
					ID:          id,
					Label:       c.Label,
					Labels:      common.Labels,
					Hostname:    common.Hostname,
					Concurrency: c.Concurrency,
				},
				Store: worker.StoreConfig{
					StorageToken: storageToken,
					StorageURL:   root.Storage.URL,
					OutputDir:    c.OutputDir,
					RemoteStore:  root.Storage.URL != "" && !strings.HasPrefix(root.Storage.URL, "file://"),
					GitUser:      root.Source.Git.User,
					GitToken:     root.Source.Git.Token,
				},
				Runtime: runtime,
				Baremetal: worker.BaremetalConfig{
					MetaDir: c.MetaDir,
				},
				Docker: worker.DockerConfig{
					Network: c.Docker.Network,
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

	cmd.Flags().String("state-dir", "", "directory for persistent worker identity and state")
	cmd.Flags().String("label", "", "worker label for task routing")
	cmd.Flags().String("master-url", "", "single piper master endpoint for the worker tunnel")
	cmd.Flags().String("worker-token", "", "worker tunnel authentication token")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	cmd.Flags().Int("concurrency", 0, "max parallel tasks")
	cmd.Flags().String("runtime", "", "execution runtime: baremetal or docker")
	cmd.Flags().String("output-dir", "", "output directory")
	cmd.Flags().String("meta-dir", "", "metadata sidecar directory (default: $TMPDIR/piper-meta)")
	cmd.Flags().String("docker-network", "", "Docker network for step containers")

	loader.MustBindFlag("workers.common.state_dir", cmd.Flags().Lookup("state-dir"))
	loader.MustBindFlag("workers.pipeline.label", cmd.Flags().Lookup("label"))
	loader.MustBindFlag("workers.common.master_url", cmd.Flags().Lookup("master-url"))
	loader.MustBindFlag("workers.common.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("workers.common.storage_token", cmd.Flags().Lookup("storage-token"))
	loader.MustBindFlag("workers.pipeline.concurrency", cmd.Flags().Lookup("concurrency"))
	loader.MustBindFlag("workers.pipeline.runtime", cmd.Flags().Lookup("runtime"))
	loader.MustBindFlag("workers.pipeline.output_dir", cmd.Flags().Lookup("output-dir"))
	loader.MustBindFlag("workers.pipeline.meta_dir", cmd.Flags().Lookup("meta-dir"))
	loader.MustBindFlag("workers.pipeline.docker.network", cmd.Flags().Lookup("docker-network"))

	return cmd
}
