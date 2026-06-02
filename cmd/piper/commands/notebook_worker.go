package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	notebookworker "github.com/piper/piper/pkg/workers/baremetal/notebook"
)

func newNotebookWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notebook-worker",
		Short: "Start a notebook worker agent on this node",
		RunE: func(cmd *cobra.Command, args []string) error {
			masterURL, _ := cmd.Flags().GetString("master")
			addr, _ := cmd.Flags().GetString("addr")
			advertiseAddr, _ := cmd.Flags().GetString("advertise-addr")
			tlsCert, _ := cmd.Flags().GetString("tls-cert")
			tlsKey, _ := cmd.Flags().GetString("tls-key")
			notebooksRoot, _ := cmd.Flags().GetString("notebooks-root")
			portRange, _ := cmd.Flags().GetString("port-range")
			mode, _ := cmd.Flags().GetString("mode")
			dockerImage, _ := cmd.Flags().GetString("docker-image")
			dockerNetwork, _ := cmd.Flags().GetString("docker-network")
			dockerCPUs, _ := cmd.Flags().GetString("docker-cpus")
			dockerMemory, _ := cmd.Flags().GetString("docker-memory")
			dockerShmSize, _ := cmd.Flags().GetString("docker-shm-size")
			dockerReadOnlyRoot, _ := cmd.Flags().GetBool("docker-read-only-root")
			dockerUser, _ := cmd.Flags().GetString("docker-user")
			dockerTmpfs, _ := cmd.Flags().GetStringArray("docker-tmpfs")
			dockerVolumeSpecs, _ := cmd.Flags().GetStringArray("docker-volume")
			dockerExtraArgs, _ := cmd.Flags().GetStringArray("docker-extra-arg")
			gpusStr, _ := cmd.Flags().GetString("gpus")
			hostname, _ := cmd.Flags().GetString("hostname")
			id, _ := cmd.Flags().GetString("id")

			var gpus []string
			if gpusStr != "" {
				for _, g := range strings.Split(gpusStr, ",") {
					if t := strings.TrimSpace(g); t != "" {
						gpus = append(gpus, t)
					}
				}
			}

			if id == "" {
				id = uuid.NewString()
			}
			if hostname == "" {
				if h, err := os.Hostname(); err == nil {
					hostname = h
				}
			}
			dockerVolumes, err := parseNotebookDockerVolumes(dockerVolumeSpecs)
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			w := notebookworker.New(notebookworker.Config{
				MasterURL:     masterURL,
				Addr:          addr,
				AdvertiseAddr: advertiseAddr,
				TLSCert:       tlsCert,
				TLSKey:        tlsKey,
				NotebooksRoot: notebooksRoot,
				PortRange:     portRange,
				Mode:          mode,
				Docker: notebookworker.DockerConfig{
					Image:        dockerImage,
					Network:      dockerNetwork,
					CPUs:         dockerCPUs,
					Memory:       dockerMemory,
					ShmSize:      dockerShmSize,
					ReadOnlyRoot: dockerReadOnlyRoot,
					User:         dockerUser,
					Tmpfs:        dockerTmpfs,
					Volumes:      dockerVolumes,
					ExtraArgs:    dockerExtraArgs,
				},
				GPUs:     gpus,
				Hostname: hostname,
				ID:       id,
			})
			return w.Run(ctx)
		},
	}

	cmd.Flags().String("master", "", "master server URL (required)")
	cmd.Flags().String("addr", ":7701", "listen address for this worker")
	cmd.Flags().String("advertise-addr", "", "URL advertised to master (default: derived from --addr)")
	cmd.Flags().String("tls-cert", "", "TLS certificate file (enables HTTPS)")
	cmd.Flags().String("tls-key", "", "TLS private key file (enables HTTPS)")
	cmd.Flags().String("notebooks-root", "", "base directory for notebook work dirs (default: ./notebooks)")
	cmd.Flags().String("port-range", "", "port range for jupyter allocation, e.g. 8888-9900 (default: 8888-9900)")
	cmd.Flags().String("mode", "", "notebook runtime mode: process or docker (default: process)")
	cmd.Flags().String("docker-image", "", "default Docker image for notebook containers")
	cmd.Flags().String("docker-network", "", "Docker network mode: bridge or none (default: bridge)")
	cmd.Flags().String("docker-cpus", "", "Docker CPU limit, e.g. 2")
	cmd.Flags().String("docker-memory", "", "Docker memory limit, e.g. 4g")
	cmd.Flags().String("docker-shm-size", "", "Docker shm size, e.g. 1g")
	cmd.Flags().Bool("docker-read-only-root", false, "run notebook containers with a read-only root filesystem")
	cmd.Flags().String("docker-user", "", "Docker container user, e.g. 1000:100")
	cmd.Flags().StringArray("docker-tmpfs", nil, "Docker tmpfs mount path; repeatable")
	cmd.Flags().StringArray("docker-volume", nil, "allowed Docker volume name=host_path:container_path[:ro|rw]; repeatable")
	cmd.Flags().StringArray("docker-extra-arg", nil, "extra Jupyter start argument for Docker mode; repeatable")
	cmd.Flags().String("gpus", "", "comma-separated GPU device indices (e.g. 0,1)")
	cmd.Flags().String("hostname", "", "hostname reported to master (default: os.Hostname)")
	cmd.Flags().String("id", "", "worker ID (default: random UUID)")
	_ = cmd.MarkFlagRequired("master")

	return cmd
}

func parseNotebookDockerVolumes(items []string) ([]notebookworker.DockerVolume, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]notebookworker.DockerVolume, 0, len(items))
	for _, item := range items {
		name, rest, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid --docker-volume %q: expected name=host_path:container_path[:ro|rw]", item)
		}
		parts := strings.Split(rest, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("invalid --docker-volume %q: expected name=host_path:container_path[:ro|rw]", item)
		}
		readOnly := true
		if len(parts) == 3 {
			switch parts[2] {
			case "ro":
				readOnly = true
			case "rw":
				readOnly = false
			default:
				return nil, fmt.Errorf("invalid --docker-volume %q: mode must be ro or rw", item)
			}
		}
		out = append(out, notebookworker.DockerVolume{
			Name:          strings.TrimSpace(name),
			HostPath:      parts[0],
			ContainerPath: parts[1],
			ReadOnly:      readOnly,
		})
	}
	return out, nil
}
