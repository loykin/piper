package commands

import (
	"context"
	"encoding/json"
	"fmt"
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
			if err := applyNotebookFlags(cmd, &root); err != nil {
				return err
			}
			selection, err := cliconfig.SelectNotebook(root)
			if err != nil {
				return err
			}
			c, common := selection.Capability, root.Worker
			hostname, mode := common.Hostname, "process"
			if selection.Infrastructure == cliconfig.InfrastructureDocker {
				mode = "docker"
			}
			if hostname == "" {
				if h, err := os.Hostname(); err == nil {
					hostname = h
				}
			}
			id, err := loadOrCreateWorkerID(common.StateDir, "notebook-"+selection.Infrastructure)
			if err != nil {
				return err
			}
			dockerVolumes := make([]notebookworker.DockerVolume, len(selection.DockerVolumes))
			for i, v := range selection.DockerVolumes {
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
					Network: selection.DockerNetwork,
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
	cmd.Flags().String("infrastructure", "", "worker infrastructure: baremetal or docker")
	cmd.Flags().String("docker-network", "", "Docker network mode: bridge or none (default: bridge)")
	cmd.Flags().String("docker-volumes", "", "Docker volumes as a JSON array")
	cmd.Flags().StringSlice("gpus", nil, "GPU device indices")
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

func applyNotebookFlags(cmd *cobra.Command, root *cliconfig.RootConfig) error {
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
			if root.Worker.Baremetal.Capabilities.Notebook == nil {
				root.Worker.Baremetal.Capabilities.Notebook = &cliconfig.NotebookCapabilityConfig{}
			}
		case cliconfig.InfrastructureDocker:
			if root.Worker.Baremetal != nil || root.Worker.K8s != nil {
				return fmt.Errorf("--infrastructure conflicts with configured worker infrastructure")
			}
			if root.Worker.Docker == nil {
				root.Worker.Docker = &cliconfig.DockerWorkerConfig{}
			}
			if root.Worker.Docker.Capabilities.Notebook == nil {
				root.Worker.Docker.Capabilities.Notebook = &cliconfig.DockerNotebookCapabilityConfig{}
			}
		default:
			return fmt.Errorf("--infrastructure must be baremetal or docker")
		}
	}
	if n := root.Worker.Baremetal; n != nil && n.Capabilities.Notebook != nil {
		applyNotebookCapabilityFlags(cmd, n.Capabilities.Notebook)
	}
	if d := root.Worker.Docker; d != nil && d.Capabilities.Notebook != nil {
		c := d.Capabilities.Notebook
		base := cliconfig.NotebookCapabilityConfig{GPUs: c.GPUs, NotebooksRoot: c.NotebooksRoot, PortRange: c.PortRange}
		applyNotebookCapabilityFlags(cmd, &base)
		c.GPUs, c.NotebooksRoot, c.PortRange = base.GPUs, base.NotebooksRoot, base.PortRange
		if cmd.Flags().Changed("docker-network") {
			d.Network, _ = cmd.Flags().GetString("docker-network")
		}
		if cmd.Flags().Changed("docker-volumes") {
			raw, _ := cmd.Flags().GetString("docker-volumes")
			if err := json.Unmarshal([]byte(raw), &c.Volumes); err != nil {
				return fmt.Errorf("--docker-volumes: %w", err)
			}
		}
	}
	return cliconfig.ValidateNotebook(*root)
}

func applyNotebookCapabilityFlags(cmd *cobra.Command, c *cliconfig.NotebookCapabilityConfig) {
	if cmd.Flags().Changed("notebooks-root") {
		c.NotebooksRoot, _ = cmd.Flags().GetString("notebooks-root")
	}
	if cmd.Flags().Changed("port-range") {
		c.PortRange, _ = cmd.Flags().GetString("port-range")
	}
	if cmd.Flags().Changed("gpus") {
		c.GPUs, _ = cmd.Flags().GetStringSlice("gpus")
	}
}
