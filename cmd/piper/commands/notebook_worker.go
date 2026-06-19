package commands

import (
	"context"
	"os"
	"os/signal"
	"strings"
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
			hostname, id, mode := common.Hostname, c.ID, c.Mode
			if hostname == "" {
				if h, err := os.Hostname(); err == nil {
					hostname = h
				}
			}
			if id == "" {
				id = stableWorkerID("notebook", hostname, effectiveNotebookMode(mode))
			}
			dockerVolumes := make([]notebookworker.DockerVolume, len(c.Docker.Volumes))
			for i, v := range c.Docker.Volumes {
				dockerVolumes[i] = notebookworker.DockerVolume{Name: v.Name, HostPath: v.HostPath, ContainerPath: v.ContainerPath, ReadOnly: v.ReadOnly}
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w := notebookworker.New(notebookworker.Config{
				AgentAddr:     common.AgentAddr,
				WorkerToken:   common.WorkerToken,
				NotebooksRoot: c.NotebooksRoot,
				PortRange:     c.PortRange,
				Mode:          mode,
				Docker: notebookworker.DockerConfig{
					Image:        c.Docker.Image,
					Network:      c.Docker.Network,
					CPUs:         c.Docker.CPUs,
					Memory:       c.Docker.Memory,
					ShmSize:      c.Docker.ShmSize,
					ReadOnlyRoot: c.Docker.ReadOnlyRoot,
					User:         c.Docker.User,
					Tmpfs:        c.Docker.Tmpfs,
					Volumes:      dockerVolumes,
					ExtraArgs:    c.Docker.ExtraArgs,
				},
				GPUs:     c.GPUs,
				Hostname: hostname,
				ID:       id,
			})
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("agent-addr", "", "piper master gRPC agent address, e.g. master:9090 (required)")
	cmd.Flags().String("notebooks-root", "", "base directory for notebook work dirs (default: ./notebooks)")
	cmd.Flags().String("port-range", "", "port range for jupyter allocation, e.g. 8888-9900 (default: 8888-9900)")
	cmd.Flags().String("mode", "", "notebook runtime mode: process or docker (default: process)")
	cmd.Flags().String("docker-image", "", "default Docker image for notebook containers")
	cmd.Flags().String("docker-network", "", "Docker network mode: bridge or none (default: bridge)")
	cmd.Flags().String("docker-cpus", "", "Docker CPU limit, e.g. 2")
	cmd.Flags().String("docker-memory", "", "Docker memory limit, e.g. 4g")
	cmd.Flags().String("docker-shm-size", "", "Docker shm size, e.g. 1g")
	cmd.Flags().Bool("docker-read-only-root", false, "run notebook containers with a read-only root filesystem")
	cmd.Flags().String("docker-user", "", "Docker container user, e.g. 1000:100")
	cmd.Flags().StringArray("docker-tmpfs", nil, "Docker tmpfs mount path; repeatable")
	cmd.Flags().String("docker-volumes", "", "Docker volumes as a JSON array")
	cmd.Flags().StringArray("docker-extra-arg", nil, "extra Jupyter start argument for Docker mode; repeatable")
	cmd.Flags().StringSlice("gpus", nil, "GPU device indices")
	cmd.Flags().String("hostname", "", "hostname reported to master (default: os.Hostname)")
	cmd.Flags().String("id", "", "worker ID (default: stable notebook-<hostname>-<mode>)")
	cmd.Flags().String("worker-token", "", "worker token for gRPC authorization")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	loader.MustBindFlag("workers.common.agent_addr", cmd.Flags().Lookup("agent-addr"))
	loader.MustBindFlag("workers.notebook.notebooks_root", cmd.Flags().Lookup("notebooks-root"))
	loader.MustBindFlag("workers.notebook.port_range", cmd.Flags().Lookup("port-range"))
	loader.MustBindFlag("workers.notebook.mode", cmd.Flags().Lookup("mode"))
	loader.MustBindFlag("workers.notebook.docker.image", cmd.Flags().Lookup("docker-image"))
	loader.MustBindFlag("workers.notebook.docker.network", cmd.Flags().Lookup("docker-network"))
	loader.MustBindFlag("workers.notebook.docker.cpus", cmd.Flags().Lookup("docker-cpus"))
	loader.MustBindFlag("workers.notebook.docker.memory", cmd.Flags().Lookup("docker-memory"))
	loader.MustBindFlag("workers.notebook.docker.shm_size", cmd.Flags().Lookup("docker-shm-size"))
	loader.MustBindFlag("workers.notebook.docker.read_only_root", cmd.Flags().Lookup("docker-read-only-root"))
	loader.MustBindFlag("workers.notebook.docker.user", cmd.Flags().Lookup("docker-user"))
	loader.MustBindFlag("workers.notebook.docker.tmpfs", cmd.Flags().Lookup("docker-tmpfs"))
	loader.MustBindFlag("workers.notebook.docker.volumes", cmd.Flags().Lookup("docker-volumes"))
	loader.MustBindFlag("workers.notebook.docker.extra_args", cmd.Flags().Lookup("docker-extra-arg"))
	loader.MustBindFlag("workers.notebook.gpus", cmd.Flags().Lookup("gpus"))
	loader.MustBindFlag("workers.common.hostname", cmd.Flags().Lookup("hostname"))
	loader.MustBindFlag("workers.notebook.id", cmd.Flags().Lookup("id"))
	loader.MustBindFlag("workers.common.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("workers.common.storage_token", cmd.Flags().Lookup("storage-token"))

	return cmd
}

func effectiveNotebookMode(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return notebookworker.RuntimeProcess
	}
	return mode
}
