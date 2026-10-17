package docker

import (
	"context"
	"fmt"
	"log/slog"

	dockerclient "github.com/moby/moby/client"
)

// ImageAPI is the subset of the Docker client EnsureImage needs.
type ImageAPI interface {
	ImageInspect(context.Context, string, ...dockerclient.ImageInspectOption) (dockerclient.ImageInspectResult, error)
	ImagePull(context.Context, string, dockerclient.ImagePullOptions) (dockerclient.ImagePullResponse, error)
}

// EnsureImage pulls image unless the daemon already has it — the same
// IfNotPresent behavior kubelet gives the k8s runtime, so a fresh docker
// installation can run an image it has never seen. Every docker driver calls
// this before ContainerCreate, which fails with "No such image" otherwise.
func EnsureImage(ctx context.Context, cli ImageAPI, image string) error {
	if _, err := cli.ImageInspect(ctx, image); err == nil {
		return nil
	}
	slog.Info("docker: pulling image", "image", image)
	resp, err := cli.ImagePull(ctx, image, dockerclient.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	defer func() { _ = resp.Close() }()
	if err := resp.Wait(ctx); err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	return nil
}
