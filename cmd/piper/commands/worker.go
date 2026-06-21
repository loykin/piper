package commands

import (
	"context"
	"fmt"
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
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	cmd.Flags().Int("concurrency", 0, "max parallel tasks")
	cmd.Flags().String("infrastructure", "", "worker infrastructure: baremetal or docker")
	cmd.Flags().String("output-dir", "", "output directory")
	cmd.Flags().String("meta-dir", "", "metadata sidecar directory (default: $TMPDIR/piper-meta)")
	cmd.Flags().String("docker-network", "", "Docker network for step containers")

	loader.MustBindFlag("worker.state_dir", cmd.Flags().Lookup("state-dir"))
	loader.MustBindFlag("worker.master_url", cmd.Flags().Lookup("master-url"))
	loader.MustBindFlag("worker.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("worker.storage_token", cmd.Flags().Lookup("storage-token"))

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
			if root.Worker.Baremetal.Capabilities.Pipeline == nil {
				root.Worker.Baremetal.Capabilities.Pipeline = &cliconfig.PipelineCapabilityConfig{}
			}
		case cliconfig.InfrastructureDocker:
			if root.Worker.Baremetal != nil || root.Worker.K8s != nil {
				return fmt.Errorf("--infrastructure conflicts with configured worker infrastructure")
			}
			if root.Worker.Docker == nil {
				root.Worker.Docker = &cliconfig.DockerWorkerConfig{}
			}
			if root.Worker.Docker.Capabilities.Pipeline == nil {
				root.Worker.Docker.Capabilities.Pipeline = &cliconfig.PipelineCapabilityConfig{}
			}
		default:
			return fmt.Errorf("--infrastructure must be baremetal or docker")
		}
	}
	var capability *cliconfig.PipelineCapabilityConfig
	if root.Worker.Baremetal != nil {
		capability = root.Worker.Baremetal.Capabilities.Pipeline
	}
	if root.Worker.Docker != nil {
		capability = root.Worker.Docker.Capabilities.Pipeline
	}
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
