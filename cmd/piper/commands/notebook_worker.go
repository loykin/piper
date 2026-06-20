package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/spf13/cobra"

	notebookworker "github.com/piper/piper/pkg/notebook/worker"
)

func newNotebookWorkerCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notebook-worker",
		Short: "Start a notebook worker agent on this node",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			if err := cliconfig.ValidateNotebook(root); err != nil {
				return err
			}
			c, common := root.Workers.Notebook, root.Workers.Common
			hostname, mode := common.Hostname, c.Mode
			if hostname == "" {
				if h, err := os.Hostname(); err == nil {
					hostname = h
				}
			}
			id, err := loadOrCreateWorkerID(common.StateDir, "notebook")
			if err != nil {
				return err
			}
			dockerVolumes := make([]notebookworker.DockerVolume, len(c.Docker.Volumes))
			for i, v := range c.Docker.Volumes {
				dockerVolumes[i] = notebookworker.DockerVolume{Name: v.Name, HostPath: v.HostPath, ContainerPath: v.ContainerPath, ReadOnly: v.ReadOnly}
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w := notebookworker.New(notebookworker.Config{
				MasterURL:     common.MasterURL,
				WorkerToken:   common.WorkerToken,
				NotebooksRoot: c.NotebooksRoot,
				PortRange:     c.PortRange,
				Mode:          mode,
				Docker: notebookworker.DockerConfig{
					Network: c.Docker.Network,
					Volumes: dockerVolumes,
				},
				GPUs:     c.GPUs,
				Hostname: hostname,
				ID:       id,
				Labels:   common.Labels,
			})
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("master-url", "", "piper master HTTP(S) URL (required)")
	cmd.Flags().String("notebooks-root", "", "base directory for notebook work dirs (default: ./notebooks)")
	cmd.Flags().String("port-range", "", "port range for jupyter allocation, e.g. 8888-9900 (default: 8888-9900)")
	cmd.Flags().String("mode", "", "notebook runtime mode: process or docker (default: process)")
	cmd.Flags().String("docker-network", "", "Docker network mode: bridge or none (default: bridge)")
	cmd.Flags().String("docker-volumes", "", "Docker volumes as a JSON array")
	cmd.Flags().StringSlice("gpus", nil, "GPU device indices")
	cmd.Flags().String("hostname", "", "hostname reported to master (default: os.Hostname)")
	cmd.Flags().String("state-dir", "", "directory for persistent worker identity and state")
	cmd.Flags().String("worker-token", "", "worker token for gRPC authorization")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	loader.MustBindFlag("workers.common.master_url", cmd.Flags().Lookup("master-url"))
	loader.MustBindFlag("workers.notebook.notebooks_root", cmd.Flags().Lookup("notebooks-root"))
	loader.MustBindFlag("workers.notebook.port_range", cmd.Flags().Lookup("port-range"))
	loader.MustBindFlag("workers.notebook.mode", cmd.Flags().Lookup("mode"))
	loader.MustBindFlag("workers.notebook.docker.network", cmd.Flags().Lookup("docker-network"))
	loader.MustBindFlag("workers.notebook.docker.volumes", cmd.Flags().Lookup("docker-volumes"))
	loader.MustBindFlag("workers.notebook.gpus", cmd.Flags().Lookup("gpus"))
	loader.MustBindFlag("workers.common.hostname", cmd.Flags().Lookup("hostname"))
	loader.MustBindFlag("workers.common.state_dir", cmd.Flags().Lookup("state-dir"))
	loader.MustBindFlag("workers.common.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("workers.common.storage_token", cmd.Flags().Lookup("storage-token"))

	return cmd
}
