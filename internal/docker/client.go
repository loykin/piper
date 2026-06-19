// Package docker provides Docker infrastructure shared by runtime drivers.
package docker

import (
	"context"
	"os"
	"path/filepath"

	dockerclient "github.com/moby/moby/client"
)

// API is the subset of the Docker client used by Piper runtime drivers.
type API interface {
	ContainerCreate(context.Context, dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error)
	ContainerInspect(context.Context, string, dockerclient.ContainerInspectOptions) (dockerclient.ContainerInspectResult, error)
	ContainerLogs(context.Context, string, dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error)
	ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error)
	ContainerStop(context.Context, string, dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error)
	ContainerRemove(context.Context, string, dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error)
	ContainerList(context.Context, dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error)
	ContainerWait(context.Context, string, dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult
}

// NewClient returns a Docker client using DOCKER_HOST when set. Otherwise it
// prefers Docker Desktop's per-user socket before falling back to the client
// defaults.
func NewClient() (*dockerclient.Client, error) {
	if os.Getenv("DOCKER_HOST") != "" {
		return dockerclient.New(dockerclient.FromEnv)
	}
	if home, err := os.UserHomeDir(); err == nil {
		sock := filepath.Join(home, ".docker", "run", "docker.sock")
		if info, err := os.Stat(sock); err == nil && !info.IsDir() {
			return dockerclient.New(dockerclient.FromEnv, dockerclient.WithHost("unix://"+sock))
		}
	}
	return dockerclient.New(dockerclient.FromEnv)
}
