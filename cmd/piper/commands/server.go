package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"

	piper "github.com/piper/piper"
	"github.com/piper/piper/pkg/source"
	notebookworker "github.com/piper/piper/pkg/workers/baremetal/notebook"
	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
	servingworker "github.com/piper/piper/pkg/workers/baremetal/serving"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
)

func newServerCmd(factory PiperFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "start the piper API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := factory()
			if err != nil {
				return err
			}
			defer func() { _ = p.Close() }()

			if p.Config().K8s.Worker {
				slog.Info("k8s pipeline worker dispatch enabled")
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			addr, _ := cmd.Flags().GetString("addr")
			if addr == "" {
				addr = p.Config().Server.Addr
			}
			if addr == "" {
				addr = ":8080"
			}

			local, _ := cmd.Flags().GetBool("local")
			localConcurrency, _ := cmd.Flags().GetInt("local-concurrency")

			if !local {
				return p.Serve(ctx, piper.ServeOption{Addr: addr})
			}

			// Embedded local worker: same pipeline worker, just pointed at localhost.
			// Worker registration may fail on first attempt (server not yet listening) —
			// the heartbeat loop handles re-registration automatically.
			masterURL := localMasterURL(addr)
			srcCfg := source.Config{
				GitUser:  viper.GetString("source.git.user"),
				GitToken: viper.GetString("source.git.token"),
			}
			wCfg := workerConfigFromSource(worker.Config{
				MasterURL:   masterURL,
				Label:       "local",
				Token:       p.Config().Server.Token,
				Concurrency: localConcurrency,
			}, srcCfg)
			w, err := worker.New(wCfg)
			if err != nil {
				return fmt.Errorf("embedded worker: %w", err)
			}
			slog.Info("embedded local worker enabled", "master", masterURL, "concurrency", localConcurrency)

			hostname, _ := os.Hostname()

			sw := servingworker.New(servingworker.Config{
				MasterURL: masterURL,
				Addr:      ":7700",
				Hostname:  hostname,
				ID:        uuid.New().String(),
			})

			nw := notebookworker.New(notebookworker.Config{
				MasterURL:     masterURL,
				Addr:          ":7701",
				Hostname:      hostname,
				ID:            uuid.New().String(),
				NotebooksRoot: p.Config().NotebookWorker.NotebooksRoot,
				PortRange:     p.Config().NotebookWorker.PortRange,
				Mode:          p.Config().NotebookWorker.Mode,
				Docker:        notebookWorkerDockerConfig(p.Config().NotebookWorker.Docker),
			})

			eg, gctx := errgroup.WithContext(ctx)
			eg.Go(func() error {
				return p.Serve(gctx, piper.ServeOption{Addr: addr})
			})
			eg.Go(func() error {
				return w.Run(gctx)
			})
			eg.Go(func() error {
				return sw.Run(gctx)
			})
			eg.Go(func() error {
				return nw.Run(gctx)
			})
			return eg.Wait()
		},
	}

	cmd.Flags().String("addr", "", "listen address (default :8080)")
	cmd.Flags().String("token", "", "bearer token required for API and UI requests")
	cmd.Flags().Bool("tls", false, "enable TLS")
	cmd.Flags().String("tls-cert", "", "TLS certificate file")
	cmd.Flags().String("tls-key", "", "TLS key file")
	cmd.Flags().Bool("local", false, "embed a local worker for dev/single-node mode")
	cmd.Flags().Int("local-concurrency", 4, "concurrency for the embedded local worker")

	mustBindPFlag("server.addr", cmd.Flags().Lookup("addr"))
	mustBindPFlag("server.token", cmd.Flags().Lookup("token"))
	mustBindPFlag("server.tls.enabled", cmd.Flags().Lookup("tls"))
	mustBindPFlag("server.tls.cert_file", cmd.Flags().Lookup("tls-cert"))
	mustBindPFlag("server.tls.key_file", cmd.Flags().Lookup("tls-key"))

	return cmd
}

func notebookWorkerDockerConfig(cfg piper.NotebookWorkerDockerConfig) notebookworker.DockerConfig {
	out := notebookworker.DockerConfig{
		Image:        cfg.Image,
		Network:      cfg.Network,
		CPUs:         cfg.CPUs,
		Memory:       cfg.Memory,
		ShmSize:      cfg.ShmSize,
		ReadOnlyRoot: cfg.ReadOnlyRoot,
		Tmpfs:        cfg.Tmpfs,
		User:         cfg.User,
		ExtraArgs:    cfg.ExtraArgs,
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
// to "http://localhost:8080" for the embedded worker to connect to.
func localMasterURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	// e.g. "0.0.0.0:8080" → "http://localhost:8080"
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		return "http://localhost" + addr[idx:]
	}
	return "http://" + addr
}
