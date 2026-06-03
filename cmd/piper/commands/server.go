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
			agentAddr, _ := cmd.Flags().GetString("agent-addr")

			local, _ := cmd.Flags().GetBool("local")
			localConcurrency, _ := cmd.Flags().GetInt("local-concurrency")

			if !local {
				return p.Serve(ctx, piper.ServeOption{Addr: addr, AgentAddr: agentAddr})
			}

			// Embedded local worker: same pipeline worker, just pointed at localhost.
			// Worker registration may fail on first attempt (server not yet listening) —
			// the heartbeat loop handles re-registration automatically.
			masterURL := localMasterURL(addr)
			srcCfg := source.Config{
				GitUser:    viper.GetString("source.git.user"),
				GitToken:   viper.GetString("source.git.token"),
				StorageURL: resolveStorageURLFromViper(),
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

			localAgentAddr := p.Config().Server.AgentAddr
			if localAgentAddr == "" {
				localAgentAddr = ":9090"
			}
			localAgentConnAddr := localAgentConnURL(localAgentAddr)

			sw := servingworker.New(servingworker.Config{
				AgentAddr: localAgentConnAddr,
				Hostname:  hostname,
				ID:        stableWorkerID("serving", hostname, "local"),
			})

			notebookMode := effectiveNotebookMode(p.Config().NotebookWorker.Mode)
			nw := notebookworker.New(notebookworker.Config{
				AgentAddr:     localAgentConnAddr,
				Hostname:      hostname,
				ID:            stableWorkerID("notebook", hostname, notebookMode, "local"),
				NotebooksRoot: p.Config().NotebookWorker.NotebooksRoot,
				PortRange:     p.Config().NotebookWorker.PortRange,
				Mode:          p.Config().NotebookWorker.Mode,
				Docker:        notebookWorkerDockerConfig(p.Config().NotebookWorker.Docker),
			})

			eg, gctx := errgroup.WithContext(ctx)
			eg.Go(func() error {
				return p.Serve(gctx, piper.ServeOption{Addr: addr, AgentAddr: localAgentAddr})
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
	cmd.Flags().String("agent-addr", "", "gRPC agent listen address (default from config)")
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

// localAgentConnURL converts a gRPC listen address like ":9090" or "0.0.0.0:9090"
// to "localhost:9090" for the embedded serving/notebook workers to dial.
func localAgentConnURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		return "localhost" + addr[idx:]
	}
	return addr
}
