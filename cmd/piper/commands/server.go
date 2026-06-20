package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	piper "github.com/piper/piper"
	cliconfig "github.com/piper/piper/cmd/piper/config"
	notebookworker "github.com/piper/piper/pkg/notebook/worker"
	worker "github.com/piper/piper/pkg/pipeline/worker"
	servingworker "github.com/piper/piper/pkg/serving/worker"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func newServerCmd(loader *cliconfig.Loader, factory PiperFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "start the piper API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			if err := cliconfig.ValidateServer(root); err != nil {
				return err
			}
			p, err := factory()
			if err != nil {
				return err
			}
			defer func() { _ = p.Close() }()

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			addr := root.Server.HTTPAddr
			local, localConcurrency := root.Server.Local.Enabled, root.Workers.Pipeline.Concurrency

			if !local {
				return p.Serve(ctx, piper.ServeOption{Addr: addr})
			}

			// Embedded local pipeline worker: connects to the local gRPC agent server.
			hostname, _ := os.Hostname()
			localMaster := localMasterURL(addr)
			pipelineID, err := loadOrCreateWorkerID(root.Workers.Common.StateDir, "local-pipeline")
			if err != nil {
				return err
			}
			servingID, err := loadOrCreateWorkerID(root.Workers.Common.StateDir, "local-serving")
			if err != nil {
				return err
			}
			notebookID, err := loadOrCreateWorkerID(root.Workers.Common.StateDir, "local-notebook")
			if err != nil {
				return err
			}

			workerToken := p.Config().Server.WorkerToken
			if root.Workers.Common.WorkerToken != "" {
				workerToken = root.Workers.Common.WorkerToken
			}
			storageToken := p.Config().Storage.Token
			if root.Workers.Common.StorageToken != "" {
				storageToken = root.Workers.Common.StorageToken
			}

			wCfg := worker.Config{
				Agent: worker.AgentConfig{
					MasterURL:   localMaster,
					WorkerToken: workerToken,
					ID:          pipelineID,
					Labels:      root.Workers.Common.Labels,
					Label:       root.Workers.Pipeline.Label,
					Concurrency: localConcurrency,
				},
				Store: worker.StoreConfig{
					StorageToken: storageToken,
					OutputDir:    root.Workers.Pipeline.OutputDir,
					StorageURL:   root.Storage.URL,
					GitUser:      root.Source.Git.User,
					GitToken:     root.Source.Git.Token,
				},
				Runtime:   worker.RuntimeType(root.Workers.Pipeline.Runtime),
				Baremetal: worker.BaremetalConfig{MetaDir: root.Workers.Pipeline.MetaDir},
				Docker:    worker.DockerConfig{Network: root.Workers.Pipeline.Docker.Network},
			}
			var w *worker.Worker
			if root.Server.Local.Pipeline {
				w, err = worker.New(wCfg)
				if err != nil {
					return fmt.Errorf("embedded worker: %w", err)
				}
			}
			slog.Info("embedded local worker enabled", "master_url", localMaster, "concurrency", localConcurrency)

			sw := servingworker.New(servingworker.Config{
				MasterURL:   localMaster,
				WorkerToken: workerToken,
				Hostname:    hostname,
				ID:          servingID,
				Labels:      root.Workers.Common.Labels,
				GPUs:        root.Workers.Serving.GPUs,
				Mode:        root.Workers.Serving.Mode,
				Docker:      servingworker.DockerConfig{Network: root.Workers.Serving.Docker.Network},
			})

			nw := notebookworker.New(notebookworker.Config{
				MasterURL:     localMaster,
				WorkerToken:   workerToken,
				Hostname:      hostname,
				ID:            notebookID,
				Labels:        root.Workers.Common.Labels,
				NotebooksRoot: p.Config().NotebookWorker.NotebooksRoot,
				PortRange:     p.Config().NotebookWorker.PortRange,
				Mode:          p.Config().NotebookWorker.Mode,
				Docker:        notebookWorkerDockerConfig(p.Config().NotebookWorker.Docker),
				GPUs:          root.Workers.Notebook.GPUs,
			})

			eg, gctx := errgroup.WithContext(ctx)
			eg.Go(func() error {
				return p.Serve(gctx, piper.ServeOption{Addr: addr})
			})
			if root.Server.Local.Pipeline {
				eg.Go(func() error { return w.Run(gctx) })
			}
			if root.Server.Local.Serving {
				eg.Go(func() error { return sw.Run(gctx) })
			}
			if root.Server.Local.Notebook {
				eg.Go(func() error { return nw.Run(gctx) })
			}
			return eg.Wait()
		},
	}

	cmd.Flags().String("addr", "", "listen address (default :8080)")
	cmd.Flags().Bool("tls", false, "enable TLS")
	cmd.Flags().String("tls-cert", "", "TLS certificate file")
	cmd.Flags().String("tls-key", "", "TLS key file")
	cmd.Flags().Bool("local", false, "embed a local worker for dev/single-node mode")
	cmd.Flags().Int("local-concurrency", 0, "concurrency for the embedded local worker")

	loader.MustBindFlag("server.http_addr", cmd.Flags().Lookup("addr"))
	loader.MustBindFlag("server.tls.enabled", cmd.Flags().Lookup("tls"))
	loader.MustBindFlag("server.tls.cert_file", cmd.Flags().Lookup("tls-cert"))
	loader.MustBindFlag("server.tls.key_file", cmd.Flags().Lookup("tls-key"))
	loader.MustBindFlag("server.local.enabled", cmd.Flags().Lookup("local"))
	loader.MustBindFlag("workers.pipeline.concurrency", cmd.Flags().Lookup("local-concurrency"))

	return cmd
}

func notebookWorkerDockerConfig(cfg piper.NotebookWorkerDockerConfig) notebookworker.DockerConfig {
	out := notebookworker.DockerConfig{
		Network: cfg.Network,
	}
	if len(cfg.Volumes) > 0 {
		out.Volumes = make([]notebookworker.DockerVolume, len(cfg.Volumes))
		for i, vol := range cfg.Volumes {
			out.Volumes[i] = notebookworker.DockerVolume{
				Name:          vol.Name,
				HostPath:      vol.HostPath,
				ContainerPath: vol.ContainerPath,
				ReadOnly:      vol.ReadOnly,
			}
		}
	}
	return out
}

// localMasterURL converts a listen address like ":8080" or "0.0.0.0:8080"
// to "http://localhost:8080" for the embedded pipeline worker to connect to.
func localMasterURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		return "http://localhost" + addr[idx:]
	}
	return "http://" + addr
}
