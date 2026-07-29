package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	notebookworker "github.com/piper/piper/pkg/notebook/worker"
)

func runNotebookWorker(root cliconfig.RootConfig) error {
	selection, err := cliconfig.SelectNotebook(root)
	if err != nil {
		return err
	}
	c, common := selection.Capability, root.Worker
	hostname := common.Hostname
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
	return notebookworker.New(notebookworker.Config{
		MasterURL: common.MasterURL, WorkerToken: common.WorkerToken,
		NotebooksRoot: c.NotebooksRoot, PortRange: c.PortRange, Infrastructure: selection.Infrastructure,
		Docker:   notebookworker.DockerConfig{Network: selection.DockerNetwork, Volumes: dockerVolumes},
		Hostname: hostname, ID: id, Labels: common.Labels,
	}).Run(ctx)
}
