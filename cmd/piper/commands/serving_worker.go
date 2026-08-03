package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	cliconfig "github.com/loykin/piper/cmd/piper/config"
	servingworker "github.com/loykin/piper/pkg/serving/worker"
)

func runServingWorker(root cliconfig.RootConfig) error {
	selection, err := cliconfig.SelectServing(root)
	if err != nil {
		return err
	}
	common := root.Worker
	hostname := common.Hostname
	if hostname == "" {
		if h, err := os.Hostname(); err == nil {
			hostname = h
		}
	}
	id, err := loadOrCreateWorkerID(common.StateDir, "serving-"+selection.Infrastructure)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return servingworker.New(servingworker.Config{
		MasterURL: common.MasterURL, WorkerToken: common.WorkerToken,
		Hostname: hostname, ID: id, Labels: common.Labels, Infrastructure: selection.Infrastructure,
		Docker: servingworker.DockerConfig{Network: selection.DockerNetwork},
	}).Run(ctx)
}
