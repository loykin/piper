package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			loader.MustBindFlag("server.http_addr", cmd.Flags().Lookup("addr"))
			loader.MustBindFlag("server.tls.enabled", cmd.Flags().Lookup("tls"))
			loader.MustBindFlag("server.tls.cert_file", cmd.Flags().Lookup("tls-cert"))
			loader.MustBindFlag("server.tls.key_file", cmd.Flags().Lookup("tls-key"))
			loader.MustBindFlag("server.local.enabled", cmd.Flags().Lookup("local"))
			loader.MustBindFlag("server.local.concurrency", cmd.Flags().Lookup("local-concurrency"))
			return loadAndLog(loader)
		},
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
			local, localConcurrency := root.Server.Local.Enabled, root.Server.Local.Concurrency

			if !local {
				return p.Serve(ctx, piper.ServeOption{Addr: addr})
			}

			// Embedded local pipeline worker: connects to the local gRPC agent server.
			hostname, _ := os.Hostname()
			localMaster := localMasterURL(addr)
			localStateDir := filepath.Join(root.Server.DataDir, ".worker-state")
			pipelineID, err := loadOrCreateWorkerID(localStateDir, "local-pipeline")
			if err != nil {
				return err
			}
			servingID, err := loadOrCreateWorkerID(localStateDir, "local-serving")
			if err != nil {
				return err
			}
			notebookID, err := loadOrCreateWorkerID(localStateDir, "local-notebook")
			if err != nil {
				return err
			}

			workerToken := p.Config().Server.WorkerToken

			wCfg := embeddedPipelineWorkerConfig(root, localMaster, pipelineID, localStateDir, localConcurrency, workerToken)
			var w *worker.Worker
			if root.Server.Local.Pipeline {
				w, err = worker.New(wCfg)
				if err != nil {
					return fmt.Errorf("embedded worker: %w", err)
				}
			}
			slog.Info("embedded local worker enabled", "master_url", localMaster, "concurrency", localConcurrency)

			sw := servingworker.New(servingworker.Config{
				MasterURL:      localMaster,
				WorkerToken:    workerToken,
				Hostname:       hostname,
				ID:             servingID,
				Infrastructure: servingworker.InfrastructureBaremetal,
			})

			nw := notebookworker.New(notebookworker.Config{
				MasterURL:      localMaster,
				WorkerToken:    workerToken,
				Hostname:       hostname,
				ID:             notebookID,
				NotebooksRoot:  p.Config().NotebookWorker.NotebooksRoot,
				PortRange:      p.Config().NotebookWorker.PortRange,
				Infrastructure: notebookworker.InfrastructureBaremetal,
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

	return cmd
}

func embeddedPipelineWorkerConfig(root cliconfig.RootConfig, localMaster, pipelineID, localStateDir string, localConcurrency int, workerToken string) worker.Config {
	return worker.Config{
		Agent: worker.AgentConfig{
			MasterURL:   localMaster,
			WorkerToken: workerToken,
			ID:          pipelineID,
			Concurrency: localConcurrency,
		},
		Store: worker.StoreConfig{
			OutputDir:        root.Server.DataDir,
			LocalStoreAccess: true,
			GitUser:          root.Source.Git.User,
			GitToken:         root.Source.Git.Token,
		},
		Runtime:   worker.RuntimeBaremetal,
		Baremetal: worker.BaremetalConfig{MetaDir: filepath.Join(localStateDir, "pipeline-meta")},
	}
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
