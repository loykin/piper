package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	worker "github.com/piper/piper/pkg/pipeline/worker"
	"github.com/spf13/cobra"
)

func newWorkerCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "start a piper pipeline worker (connects to master via gRPC)",
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			loader.MustBindFlag("worker.state_dir", cmd.Flags().Lookup("state-dir"))
			loader.MustBindFlag("worker.master_url", cmd.Flags().Lookup("master-url"))
			loader.MustBindFlag("worker.worker_token", cmd.Flags().Lookup("worker-token"))
			return loadAndLog(loader)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			if err := applyPipelineFlags(cmd, &root); err != nil {
				return err
			}
			selection, err := cliconfig.SelectPipeline(root)
			if err != nil {
				return err
			}
			common := root.Worker
			id, err := loadOrCreateWorkerID(common.StateDir, "pipeline-"+selection.Infrastructure)
			if err != nil {
				return err
			}

			c := selection.Capability
			runtime := worker.RuntimeType(selection.Infrastructure)

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
					OutputDir: c.OutputDir,
					GitUser:   root.Source.Git.User,
					GitToken:  root.Source.Git.Token,
				},
				Runtime: runtime,
				Baremetal: worker.BaremetalConfig{
					MetaDir: c.MetaDir,
				},
				Docker: worker.DockerConfig{
					Network: selection.DockerNetwork,
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
	cmd.Flags().String("storage-token", "", "deprecated; artifact storage is provided by the master task payload")
	cmd.Flags().Int("concurrency", 0, "max parallel tasks")
	cmd.Flags().String("infrastructure", "", "worker infrastructure: baremetal or docker")
	cmd.Flags().String("output-dir", "", "output directory")
	cmd.Flags().String("meta-dir", "", "metadata sidecar directory (default: $TMPDIR/piper-meta)")
	cmd.Flags().String("docker-network", "", "Docker network for step containers")
	_ = cmd.Flags().MarkDeprecated("storage-token", "artifact storage is configured on the master and delivered with each task")

	return cmd
}

func applyPipelineFlags(cmd *cobra.Command, root *cliconfig.RootConfig) error {
	runtime, _ := cmd.Flags().GetString("infrastructure")
	if cmd.Flags().Changed("infrastructure") {
		switch runtime {
		case cliconfig.InfrastructureBaremetal:
			if root.Worker.Docker != nil || root.Worker.K8s != nil {
				return fmt.Errorf("--infrastructure conflicts with configured worker infrastructure")
			}
			if root.Worker.Baremetal == nil {
				root.Worker.Baremetal = &cliconfig.BaremetalWorkerConfig{}
			}
			if root.Worker.Capabilities.Pipeline == nil {
				root.Worker.Capabilities.Pipeline = &cliconfig.PipelineCapabilityConfig{}
			}
		case cliconfig.InfrastructureDocker:
			if root.Worker.Baremetal != nil || root.Worker.K8s != nil {
				return fmt.Errorf("--infrastructure conflicts with configured worker infrastructure")
			}
			if root.Worker.Docker == nil {
				root.Worker.Docker = &cliconfig.DockerWorkerConfig{}
			}
			if root.Worker.Capabilities.Pipeline == nil {
				root.Worker.Capabilities.Pipeline = &cliconfig.PipelineCapabilityConfig{}
			}
		default:
			return fmt.Errorf("--infrastructure must be baremetal or docker")
		}
	}
	capability := root.Worker.Capabilities.Pipeline
	if capability != nil {
		if cmd.Flags().Changed("label") {
			capability.Label, _ = cmd.Flags().GetString("label")
		}
		if cmd.Flags().Changed("concurrency") {
			capability.Concurrency, _ = cmd.Flags().GetInt("concurrency")
		}
		if cmd.Flags().Changed("output-dir") {
			capability.OutputDir, _ = cmd.Flags().GetString("output-dir")
		}
		if cmd.Flags().Changed("meta-dir") {
			capability.MetaDir, _ = cmd.Flags().GetString("meta-dir")
		}
	}
	if root.Worker.Docker != nil && cmd.Flags().Changed("docker-network") {
		root.Worker.Docker.Network, _ = cmd.Flags().GetString("docker-network")
	}
	return cliconfig.ValidatePipeline(*root)
}
