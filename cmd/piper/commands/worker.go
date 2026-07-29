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
	return &cobra.Command{
		Use:     "worker",
		Short:   "start the worker configured in piper.yaml",
		PreRunE: makePreRunE(loader),
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			return runConfiguredWorker(root)
		},
	}
}

func runConfiguredWorker(root cliconfig.RootConfig) error {
	if err := cliconfig.ValidateWorker(root); err != nil {
		return err
	}
	if root.Worker.K8s != nil {
		return runK8sWorker(root)
	}
	count := 0
	role := ""
	if root.Worker.Capabilities.Pipeline != nil {
		count++
		role = "pipeline"
	}
	if root.Worker.Capabilities.Notebook != nil {
		count++
		role = "notebook"
	}
	if root.Worker.Capabilities.Serving != nil {
		count++
		role = "serving"
	}
	if count != 1 {
		return fmt.Errorf("config: host worker requires exactly one pipeline, notebook, or serving capability")
	}
	switch role {
	case "pipeline":
		return runPipelineWorker(root)
	case "notebook":
		return runNotebookWorker(root)
	default:
		return runServingWorker(root)
	}
}

func runPipelineWorker(root cliconfig.RootConfig) error {
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
	cfg := worker.Config{
		Agent: worker.AgentConfig{
			MasterURL: common.MasterURL, WorkerToken: common.WorkerToken, ID: id,
			Label: c.Label, Labels: common.Labels, Hostname: common.Hostname, Concurrency: c.Concurrency,
		},
		Store:     worker.StoreConfig{OutputDir: c.OutputDir, GitUser: root.Source.Git.User, GitToken: root.Source.Git.Token},
		Runtime:   worker.RuntimeType(selection.Infrastructure),
		Baremetal: worker.BaremetalConfig{MetaDir: c.MetaDir},
		Docker:    worker.DockerConfig{Network: selection.DockerNetwork},
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	w, err := worker.New(cfg)
	if err != nil {
		return err
	}
	return w.Run(ctx)
}
