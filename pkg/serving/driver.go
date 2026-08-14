package serving

import (
	"context"

	"github.com/loykin/piper/internal/artifact"
)

// Driver abstracts the installation-owned runtime that executes a service.
// Piper calls it in-process; implementations talk directly to Kubernetes,
// Docker, or the local process supervisor.
type Driver interface {
	// ArtifactTarget declares how this driver expects the model artifact to be
	// delivered. K8s returns TargetRemote because pods cannot access Piper's
	// local filesystem; Docker and baremetal return TargetLocal.
	ArtifactTarget() artifact.Target
	Deploy(ctx context.Context, spec ModelService, art artifact.Resolved, yamlStr string) (*Service, error)
	Stop(ctx context.Context, svc *Service) error
}
